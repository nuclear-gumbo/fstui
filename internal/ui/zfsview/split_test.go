// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/config"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// splitPools returns two pools rendered wide enough for list and detail
// side by side, the list focused.
func splitPools(t *testing.T) *Pools {
	t.Helper()
	p := opened(t, "healthy", "degraded")
	pressKey(p, "esc")
	render(p, config.Default().UI.SplitFrom, 30)
	if !p.geo.split || p.focus {
		t.Fatalf("not a split with the list focused: split %v focus %v", p.geo.split, p.focus)
	}
	return p
}

// In the split layout the detail shows the selected pool and keeps its
// tab while the cursor moves.
func TestSplitDetailFollowsCursor(t *testing.T) {
	p := splitPools(t)
	p.detail.tab = tabHistory
	pressKey(p, "j")
	if p.detail.pool != p.poll.data[1].Pool.Name || p.detail.tab != tabHistory {
		t.Fatalf("detail shows %s on tab %d after j", p.detail.pool, p.detail.tab)
	}
	out := render(p, config.Default().UI.SplitFrom, 30)
	top := strings.Split(out, "\n")[0]
	if !strings.Contains(top, "+-pools-") || !strings.Contains(top, "| History |") {
		t.Errorf("top edges: %q", top)
	}
}

// enter moves the focus into the detail, which then takes the keys;
// esc gives it back to the list without closing the detail.
func TestSplitFocus(t *testing.T) {
	p := splitPools(t)
	pressKey(p, "enter")
	if !p.focus {
		t.Fatal("enter did not focus the detail")
	}
	pressKey(p, "l")
	if p.detail.tab != tabSpace {
		t.Errorf("l in the detail selected tab %d", p.detail.tab)
	}
	pressKey(p, "esc")
	if p.focus || p.detail == nil {
		t.Errorf("esc: focus %v, detail %v", p.focus, p.detail != nil)
	}
	pressKey(p, "l") // the list has no tabs; the detail keeps its tab
	if p.detail.tab != tabSpace {
		t.Errorf("l in the list changed the detail to tab %d", p.detail.tab)
	}
}

// Clicks find the panel under the pointer: a tab in the detail's top
// edge, a row in the list.
func TestSplitClicks(t *testing.T) {
	p := splitPools(t)
	top := strings.Split(render(p, config.Default().UI.SplitFrom, 30), "\n")[0]
	// The shortened title holds a "…", so count cells, not bytes.
	p.Update(ui.ClickMsg{X: lipgloss.Width(top[:strings.Index(top, "Features")]), Y: 0})
	if !p.focus || p.detail.tab != tabFeatures {
		t.Fatalf("click on Features: focus %v, tab %d", p.focus, p.detail.tab)
	}
	_, cmd := p.Update(ui.ClickMsg{X: 3, Y: listTop + 1})
	run(p, cmd)
	if p.focus || p.cursor != 1 || p.detail.pool != p.poll.data[1].Pool.Name {
		t.Errorf("click on the second pool: focus %v, cursor %d, detail %s", p.focus, p.cursor, p.detail.pool)
	}
}

// Datasets uses the same layout: the detail follows the tree cursor.
func TestSplitDatasets(t *testing.T) {
	v := loadedDatasets(t)
	out := ansi.Strip(v.Render(display(), config.Default().UI.SplitFrom, 20))
	if !v.geo.split || v.detail == nil || !strings.Contains(strings.Split(out, "\n")[0], "| Capacity |") {
		t.Fatalf("no split detail:\n%s", out)
	}
	first := v.detail.ds
	_, cmd := v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if v.detail.ds == first || v.focus || cmd == nil {
		t.Errorf("j: detail %s (was %s), focus %v, cmd %v", v.detail.ds, first, v.focus, cmd != nil)
	}
}

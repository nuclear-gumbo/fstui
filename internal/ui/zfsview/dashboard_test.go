// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// dashboardOver builds a dashboard over loaded views: pools from the
// golden scenarios, the captured events, a fake I/O stream and the ARC.
func dashboardOver(t *testing.T, panels ...string) (*Dashboard, *fakeEvents, *fakeIO) {
	t.Helper()
	pools := loaded(t, defaults(), golden(t, "healthy"), golden(t, "scrub-in-progress"))
	evs, src := loadedEvents(t)
	io := &fakeIO{samples: goldenIOSamples(t), cancelled: make(chan struct{})}
	ioView := NewIO(io, func() time.Time { return now })
	arc := NewARC(func() (zfs.ARCStats, error) { return goldenARC(t, "no-l2arc.txt"), nil }, func() time.Time { return now })
	arc.Update(arc.Init()())
	d := NewDashboard(pools, evs, ioView, arc, DashboardOptions{Panels: panels, Now: func() time.Time { return now }})
	return d, src, io
}

func renderDash(d *Dashboard, w, h int) string { return ansi.Strip(d.Render(display(), w, h)) }

func TestDashboardPanels(t *testing.T) {
	d, _, _ := dashboardOver(t, "health", "events", "io", "arc", "scans")
	out := renderDash(d, 220, 60)
	for _, want := range []string{
		"+-health", "+-events", "+-io", "+-arc", "+-scans",
		"fstuitest_g_healthy", "Checksum error", "Waiting for zpool iostat", "hit ratio now", "fstuitest_g_scrub",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard lacks %q:\n%s", want, out)
		}
	}
	// Three columns from 200 cells: health, events and io share the top.
	top := strings.Split(out, "\n")[0]
	if !strings.Contains(top, "+-health") || !strings.Contains(top, "+-events") || !strings.Contains(top, "+-io") {
		t.Errorf("top row: %q", top)
	}
	if top := strings.Split(renderDash(d, 100, 40), "\n")[0]; strings.Contains(top, "+-events") {
		t.Errorf("one column below 120 cells: %q", top)
	}
}

// A new error marks the events panel until a key is pressed.
func TestDashboardMarksNewProblems(t *testing.T) {
	d, src, _ := dashboardOver(t, "events")
	if !strings.Contains(renderDash(d, 100, 20), "events: 20 new errors") {
		t.Fatalf("captured errors not marked:\n%s", renderDash(d, 100, 20))
	}
	d.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if out := renderDash(d, 100, 20); strings.Contains(out, "new") {
		t.Fatalf("still marked after a key:\n%s", out)
	}
	err := src.evs[0]
	err.ID, err.Severity = src.evs[len(src.evs)-1].ID+1, 4
	src.evs = append(src.evs, err)
	d.evs.Update(d.evs.poll.refresh()())
	if !strings.Contains(renderDash(d, 100, 20), "events: 1 new error") {
		t.Errorf("a new error not marked:\n%s", renderDash(d, 100, 20))
	}
}

// Shown, the dashboard makes sure zpool iostat runs and reads the ARC
// and events faster; left, zpool iostat keeps running, so the graphs
// have no gaps.
func TestDashboardActivatesItsViews(t *testing.T) {
	d, _, io := dashboardOver(t, "io")
	if d.Init() != nil {
		t.Error("the dashboard reads by itself")
	}
	cmd := d.Activate()
	if cmd == nil || !d.io.running || d.arc.poll.interval != arcShown || d.evs.poll.interval != eventsShown {
		t.Fatalf("activate: running %v, arc %v, events %v", d.io.running, d.arc.poll.interval, d.evs.poll.interval)
	}
	d.Deactivate()
	select {
	case <-io.cancelled:
		t.Fatal("leaving the dashboard stopped zpool iostat")
	case <-time.After(100 * time.Millisecond):
	}
	if !d.io.running {
		t.Fatal("leaving the dashboard stopped zpool iostat")
	}
}

// viewOf runs a command and returns the view it asks the root to show.
func viewOf(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	if cmd == nil {
		return 0
	}
	switch m := cmd().(type) {
	case ui.SwitchViewMsg:
		return m.View
	case tea.BatchMsg:
		for _, c := range m {
			if v := viewOf(t, c); v != 0 {
				return v
			}
		}
	}
	return 0
}

func dashKey(d *Dashboard, k string) tea.Cmd {
	msg := tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	if k == "enter" {
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	_, cmd := d.Update(msg)
	return cmd
}

// The health panel selects pools and opens the selected one in Pools.
func TestDashboardHealthOpensPool(t *testing.T) {
	d, _, _ := dashboardOver(t, "health", "events", "io", "arc")
	renderDash(d, 140, 40)
	dashKey(d, "j")
	if d.pools.cursor != 1 {
		t.Fatalf("j in health: pool %d", d.pools.cursor)
	}
	if v := viewOf(t, dashKey(d, "enter")); v != poolsView || d.pools.detail == nil || !d.pools.focus {
		t.Errorf("enter: view %d, detail open %v", v, d.pools.detail != nil)
	}
}

// The events panel scrolls, and enter opens the event in Events.
func TestDashboardEventsOpenEvent(t *testing.T) {
	d, _, _ := dashboardOver(t, "health", "events")
	renderDash(d, 140, 40)
	dashKey(d, "l")
	if d.focused() != "events" {
		t.Fatalf("l focused %q", d.focused())
	}
	dashKey(d, "j")
	dashKey(d, "j")
	want := d.evShown[2]
	if v := viewOf(t, dashKey(d, "enter")); v != eventsView || !d.evs.focus || d.evs.shown()[d.evs.cursor].ID != want {
		t.Errorf("enter: view %d, detail %v", v, d.evs.focus)
	}
}

// A click focuses the panel under it and selects the row.
func TestDashboardClick(t *testing.T) {
	d, _, _ := dashboardOver(t, "health", "events")
	renderDash(d, 140, 40)
	r := d.rects[1]
	d.Update(ui.ClickMsg{X: r.x + 5, Y: r.y + 4}) // the fourth event
	if d.focus != 1 || d.evCursor != 3 {
		t.Errorf("click: focus %d, event %d", d.focus, d.evCursor)
	}
}

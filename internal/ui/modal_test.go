// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// boxModal draws a framed box of numbered, styled rows.
type boxModal struct {
	rows int
	busy bool
}

func (b *boxModal) Busy() bool          { return b.busy }
func (b *boxModal) Keys() []key.Binding { return nil }

func (b *boxModal) Update(msg tea.Msg) (Modal, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		return nil, nil
	}
	return b, nil
}

func (b *boxModal) Render(d Display, _, _ int) string {
	var lines []string
	for i := range b.rows {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render("row ")+string(rune('a'+i)))
	}
	return Panel{Title: "box", Focused: true}.Render(d, strings.Join(lines, "\n"), 40, b.rows+2)
}

func TestModalOverlayKeepsFrameAndRows(t *testing.T) {
	scr := &fakeScreen{name: "Pools"}
	m := sized(newModel(colorprofile.TrueColor, scr), 120, 30)
	out, _ := m.Update(OpenModalMsg{Modal: &boxModal{rows: 6}})
	m = out.(Model) //nolint:forcetypeassert // Update always returns Model
	content := m.View().Content
	lines := strings.Split(ansi.Strip(content), "\n")
	if len(lines) != 30 {
		t.Fatalf("%d lines", len(lines))
	}
	var box []string
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 120 {
			t.Errorf("line %d is %d cells wide: %q", i+1, w, l)
		}
		if j := strings.Index(l, "╭─box"); j >= 0 || len(box) > 0 && len(box) < 8 {
			box = append(box, l)
		}
	}
	if len(box) != 8 || !strings.Contains(box[1], "│row a") || !strings.Contains(box[6], "│row f") || !strings.Contains(box[7], "╰────") {
		t.Fatalf("box rows:\n%s", strings.Join(box, "\n"))
	}
}

func TestModalTakesKeysAndQuitWaitsWhileBusy(t *testing.T) {
	scr := &fakeScreen{name: "Pools"}
	m := sized(newModel(colorprofile.ASCII, scr), 100, 30)
	md := &boxModal{rows: 2, busy: true}
	out, _ := m.Update(OpenModalMsg{Modal: md})
	m = out.(Model) //nolint:forcetypeassert // Update always returns Model
	before := len(scr.got)
	m, _ = press(t, m, "j")
	if len(scr.got) != before {
		t.Error("a key reached the screen while a modal was open")
	}
	if _, cmd := press(t, m, "ctrl+c"); isQuit(cmd) {
		t.Error("ctrl+c quit while a change was running")
	}
	md.busy = false
	if _, cmd := press(t, m, "ctrl+c"); !isQuit(cmd) {
		t.Error("ctrl+c did not quit once the change finished")
	}
	m, _ = press(t, m, "esc")
	if m.modal != nil {
		t.Error("esc did not close the modal")
	}
}

// fakeActions records what the root asked of the write module.
type fakeActions struct{ asked []string }

func (f *fakeActions) Menu(Target) (Modal, tea.Cmd) {
	f.asked = append(f.asked, "menu")
	return &boxModal{rows: 1}, nil
}

func (f *fakeActions) Snapshot(Target) (Modal, tea.Cmd) {
	f.asked = append(f.asked, "snapshot")
	return &boxModal{rows: 1}, nil
}
func (f *fakeActions) Offered(Target) []key.Binding { return nil }

func (f *fakeActions) CanSnapshot(Target) bool { return true }

func (f *fakeActions) NoChanges(_ string, empty bool, _ string) Modal {
	f.asked = append(f.asked, map[bool]string{true: "empty", false: "none"}[empty])
	return &boxModal{rows: 1}
}
func (f *fakeActions) ToggleWrite() tea.Cmd { return nil }

// selScreen is a fakeScreen with a selection, or an empty list.
type selScreen struct {
	fakeScreen
	empty bool
}

func (s *selScreen) Selection() (Target, bool) {
	return Target{Backend: "zfs", Kind: "filesystem", Names: []string{"tank/a"}}, !s.empty
}

// a must answer in every view, even where nothing can be changed.
func TestAAnswersInEveryView(t *testing.T) {
	for _, tc := range []struct {
		screen Screen
		want   string
	}{
		{&fakeScreen{name: "Pools"}, "none"},
		{&selScreen{fakeScreen: fakeScreen{name: "Snapshots"}, empty: true}, "empty"},
		{&selScreen{fakeScreen: fakeScreen{name: "Datasets"}}, "menu"},
	} {
		acts := new(fakeActions)
		m := New(Session{Host: termtext.New("h")}, []Screen{tc.screen}, Options{Profile: colorprofile.ASCII, Now: func() time.Time { return epoch }, Actions: acts})
		m = sized(m, 120, 30)
		m, _ = press(t, m, "a")
		if m.modal == nil || len(acts.asked) != 1 || acts.asked[0] != tc.want {
			t.Errorf("%s: a asked %v, modal %v; want %s", tc.screen.Name(), acts.asked, m.modal, tc.want)
		}
	}
}

func TestChangeKeysShowInTheHintBar(t *testing.T) {
	m := New(Session{Host: termtext.New("h")}, []Screen{&selScreen{fakeScreen: fakeScreen{name: "Datasets"}}},
		Options{Profile: colorprofile.ASCII, Now: func() time.Time { return epoch }, Actions: new(fakeActions)})
	lines := strings.Split(ansi.Strip(sized(m, 120, 30).View().Content), "\n")
	if hints := lines[len(lines)-2]; !strings.HasPrefix(hints, "a changes  n new snapshot") {
		t.Errorf("hint bar = %q", hints)
	}
	m, _ = press(t, sized(m, 120, 30), "n")
	if m.modal == nil {
		t.Error("n opened nothing in Datasets")
	}
}

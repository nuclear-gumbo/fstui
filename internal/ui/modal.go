// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Modal is a dialog drawn over the active screen, such as the preview
// and confirmation of a change. While one is open it takes every key and
// click; other messages still reach the screens as well, so their reads
// keep arriving.
//
// The ui package knows nothing of what a modal does: the write module
// builds them, and a build without it has none.
type Modal interface {
	// Update returns the modal to keep showing, or nil to close it.
	Update(msg tea.Msg) (Modal, tea.Cmd)
	// Render draws the modal, frame included, in at most width x height.
	Render(d Display, width, height int) string
	Keys() []key.Binding
	// Busy reports that a change is running: quitting is refused until
	// it finishes, so its audit record is complete.
	Busy() bool
}

// OpenModalMsg shows a modal over the active screen.
type OpenModalMsg struct{ Modal Modal }

// ModeMsg changes the mode word in the status bar, e.g. "WRITE".
type ModeMsg struct{ Mode string }

// ChangedMsg tells every screen that a change ran: screens reload what
// they show now instead of at their next refresh.
type ChangedMsg struct{}

// Target is what the user has selected in a view, as far as changes are
// concerned: the backend, the kind of object and the names.
type Target struct {
	Backend string   // "zfs"
	Kind    string   // "pool", "device", "filesystem", "volume", "snapshot"
	Names   []string // full names; one unless several are marked
	Parent  string   // the pool of a device
}

// Selector is implemented by screens whose selection changes can act on.
type Selector interface {
	Selection() (Target, bool)
}

// Reasoner is implemented by a screen that can say why its selection
// offers no changes, such as a schedule fstui did not write.
type Reasoner interface {
	NoChangeReason() string
}

// Selects is implemented by a Selector that offers changes only some of
// the time, such as on one of its tabs.
type Selects interface {
	Selects() bool
}

// Actions is the write module's hook into the UI. Without the module,
// Options.Actions is nil and fstui offers no changes.
type Actions interface {
	// Menu returns a modal listing the changes offered for t, or nil,
	// and a command that finishes it, such as reading permissions.
	Menu(t Target) (Modal, tea.Cmd)
	// Snapshot returns the form for a new snapshot of t (the n key), or
	// nil when t cannot have one.
	Snapshot(t Target) (Modal, tea.Cmd)
	// CanSnapshot reports whether n makes a snapshot for t.
	CanSnapshot(t Target) bool
	// Offered lists the changes for t with their keys, for the help.
	Offered(t Target) []key.Binding
	// NoChanges returns a modal saying where changes are made, for the
	// a key in a view that offers none; empty is set for a view that
	// offers changes but has nothing selected, such as an empty list.
	// reason, when the view gives one, says why the selection has none.
	NoChanges(view string, empty bool, reason string) Modal
	// ToggleWrite starts switching between read-only and write mode.
	ToggleWrite() tea.Cmd
}

// modalKey handles a key while a modal is open: ctrl+c quits unless a
// change is running; everything else goes to the modal.
func (m Model) modalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Get(keys.Quit)) && !m.modal.Busy() {
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.modal, cmd = m.modal.Update(msg)
	return m, cmd
}

// writeKeys are the root's bindings that exist only with the write module.
func (m Model) writeKeys() []key.Binding {
	if m.opts.Actions == nil {
		return nil
	}
	return []key.Binding{keys.Get(keys.Actions), keys.Get(keys.NewSnapshot), keys.Get(keys.WriteMode)}
}

// selection is the active screen's selection, if it has one.
func (m Model) selection() (Target, bool) {
	if len(m.screens) == 0 {
		return Target{}, false
	}
	sel, ok := m.screens[m.active].(Selector)
	if !ok {
		return Target{}, false
	}
	return sel.Selection()
}

func (m Model) isSelector() bool {
	if len(m.screens) == 0 {
		return false
	}
	if s, ok := m.screens[m.active].(Selects); ok {
		return s.Selects()
	}
	_, ok := m.screens[m.active].(Selector)
	return ok
}

// changeHints are shown first in the hint bar of a view whose selection
// can be changed, so the changes can be found without the help.
func (m Model) changeHints() []key.Binding {
	if m.opts.Actions == nil {
		return nil
	}
	if !m.isSelector() {
		return nil
	}
	hints := []key.Binding{keys.As(keys.Actions, "changes")}
	if t, ok := m.selection(); !ok || m.opts.Actions.CanSnapshot(t) {
		hints = append(hints, keys.Get(keys.NewSnapshot))
	}
	return hints
}

// actionKey handles the write module's keys: a for the changes to the
// selection (or, where there is none, where changes are made), n for a
// new snapshot of it, W for write mode.
func (m Model) actionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.opts.Actions == nil || len(m.screens) == 0 {
		return m, nil, false
	}
	t, selected := m.selection()
	var md Modal
	var cmd tea.Cmd
	switch {
	case key.Matches(msg, keys.Get(keys.WriteMode)):
		return m, m.opts.Actions.ToggleWrite(), true
	case key.Matches(msg, keys.Get(keys.Actions)) && selected:
		md, cmd = m.opts.Actions.Menu(t)
	case key.Matches(msg, keys.Get(keys.NewSnapshot)) && selected && m.opts.Actions.CanSnapshot(t):
		md, cmd = m.opts.Actions.Snapshot(t)
	case key.Matches(msg, keys.Get(keys.Actions)):
		md = m.noChanges()
	default:
		return m, nil, false // n means nothing to the root here
	}
	if md == nil {
		md = m.noChanges()
	}
	m.modal = md
	return m, cmd, true
}

// noChanges is the message for the a key where nothing can change, with the
// active screen's reason when it gives one.
func (m Model) noChanges() Modal {
	var reason string
	if r, ok := m.screens[m.active].(Reasoner); ok {
		reason = r.NoChangeReason()
	}
	return m.opts.Actions.NoChanges(m.screens[m.active].Name(), m.isSelector(), reason)
}

// overlay draws box centred over base, both width x height cells.
func overlay(base, box string, width, height int) string {
	lines := strings.Split(base, "\n")
	bl := strings.Split(box, "\n")
	bw := 0
	for _, l := range bl {
		bw = max(bw, lipgloss.Width(l))
	}
	x := max((width-bw)/2, 0)
	y := max((height-len(bl))/2, 0)
	for i, l := range bl {
		if y+i >= len(lines) {
			break
		}
		row := lines[y+i]
		left := ansi.Truncate(row, x, "")
		left += strings.Repeat(" ", x-lipgloss.Width(left))
		right := ansi.TruncateLeft(row, x+bw, "")
		lines[y+i] = left + l + strings.Repeat(" ", bw-lipgloss.Width(l)) + right
	}
	return strings.Join(lines, "\n")
}

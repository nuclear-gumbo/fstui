// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Changes is view 8, Plans: saved plans and the audit log, as two tabs.
type Changes struct {
	plans *Plans
	audit *Audit
	tab   int
	width int // as last drawn, for clicks on the tabs
}

const (
	tabPlans = iota
	tabAudit
)

var changesTabs = []string{"saved", "audit log"}

// NewChanges returns view 8 for a session.
func NewChanges(s *Session) *Changes { return &Changes{plans: NewPlans(s), audit: NewAudit(s)} }

// Number implements ui.Numbered.
func (v *Changes) Number() int { return plansView }

// Name implements ui.Screen.
func (v *Changes) Name() string { return "Plans" }

// Framed implements ui.Framed: the panel carries the tabs.
func (v *Changes) Framed() bool { return true }

// Nested implements ui.Screen.
func (v *Changes) Nested() bool { return false }

// Typing implements ui.Typing: the audit filter takes text.
func (v *Changes) Typing() bool { return v.tab == tabAudit && v.audit.typing }

// Init implements ui.Screen.
func (v *Changes) Init() tea.Cmd { return v.plans.Init() }

// Activate implements ui.Activator.
func (v *Changes) Activate() tea.Cmd { return tea.Batch(v.plans.Activate(), v.audit.load()) }

// Keys implements ui.Screen.
func (v *Changes) Keys() []key.Binding {
	tabs := []key.Binding{keys.As(keys.Right, "next tab"), keys.As(keys.Left, "previous tab")}
	if v.tab == tabAudit {
		return append(v.audit.Keys(), tabs...)
	}
	return append(v.plans.Keys(), tabs...)
}

// Status implements ui.Screen.
func (v *Changes) Status() ui.Status {
	if v.tab == tabAudit {
		return v.audit.Status()
	}
	return v.plans.Status()
}

// Update implements ui.Screen. Messages other than input go to both
// tabs; keys and clicks to the one shown.
func (v *Changes) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if !v.Typing() && v.switchTab(msg) {
			return v, v.shown()
		}
		return v, v.toShown(msg)
	case ui.ClickMsg:
		if msg.Y == 0 {
			if t := v.panel().TabAt(v.width, msg.X); t >= 0 {
				v.tab = t
				return v, v.shown()
			}
			return v, nil
		}
		// Inside the border: the tabs' own coordinates.
		return v, v.toShown(ui.ClickMsg{X: msg.X - 1, Y: msg.Y - 1})
	case ui.ScrollMsg:
		return v, v.toShown(msg)
	}
	_, a := v.plans.Update(msg)
	b := v.audit.Update(msg)
	return v, tea.Batch(a, b)
}

func (v *Changes) switchTab(msg tea.KeyPressMsg) bool {
	switch {
	case key.Matches(msg, keys.Get(keys.Right)):
		v.tab = (v.tab + 1) % len(changesTabs)
	case key.Matches(msg, keys.Get(keys.Left)):
		v.tab = (v.tab + len(changesTabs) - 1) % len(changesTabs)
	default:
		return false
	}
	return true
}

// shown reloads the tab just switched to.
func (v *Changes) shown() tea.Cmd {
	if v.tab == tabAudit {
		return v.audit.load()
	}
	return v.plans.load()
}

func (v *Changes) toShown(msg tea.Msg) tea.Cmd {
	if v.tab == tabAudit {
		return v.audit.Update(msg)
	}
	_, cmd := v.plans.Update(msg)
	return cmd
}

func (v *Changes) panel() ui.Panel {
	return ui.Panel{Title: "plans", Tabs: changesTabs, Active: v.tab, Focused: true}
}

// Render implements ui.Screen.
func (v *Changes) Render(d ui.Display, width, height int) string {
	v.width = width
	iw, ih := ui.Inner(width, height)
	var body string
	if v.tab == tabAudit {
		body = v.audit.Render(d, iw, ih)
	} else {
		body = v.plans.Render(d, iw, ih)
	}
	return v.panel().Render(d, body, width, height)
}

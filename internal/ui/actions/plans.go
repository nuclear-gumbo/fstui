// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// plansView is the view number of Changes (saved plans and the audit
// log): 8, free between Events and Settings.
const plansView = 8

// Plans lists the saved plans: when each was made, its state, its tier and what
// it does. enter applies one, which builds it again against the current state
// first.
type Plans struct {
	s        *Session
	list     []mutate.Entry
	err      error
	loaded   time.Time
	cursor   int
	deleting bool // d was pressed once
	message  string
}

// NewPlans returns the Plans view of a session.
func NewPlans(s *Session) *Plans { return &Plans{s: s} }

type plansMsg struct {
	list []mutate.Entry
	err  error
	at   time.Time
}

type reappliedMsg struct {
	sha  string
	plan *mutate.Plan
	err  error
}

// Number implements ui.Numbered.
func (v *Plans) Number() int { return plansView }

// Name implements ui.Screen.
func (v *Plans) Name() string { return "Plans" }

// Nested implements ui.Screen.
func (v *Plans) Nested() bool { return false }

// Init implements ui.Screen.
func (v *Plans) Init() tea.Cmd { return v.load() }

// Activate implements ui.Activator: plans may have been saved meanwhile.
func (v *Plans) Activate() tea.Cmd { return v.load() }

func (v *Plans) load() tea.Cmd {
	store, now := v.s.store, v.s.now
	return func() tea.Msg {
		list, err := store.List()
		return plansMsg{list: list, err: err, at: now()}
	}
}

// Keys implements ui.Screen.
func (v *Plans) Keys() []key.Binding {
	return []key.Binding{
		keys.As(keys.Select, "apply"), keys.As(keys.Discard, "delete"), keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"),
		keys.Get(keys.Refresh),
	}
}

// Status implements ui.Screen.
func (v *Plans) Status() ui.Status {
	st := ui.Status{Refreshed: v.loaded, Figure: fmt.Sprintf("%d saved %s", len(v.list), map[bool]string{true: "plan", false: "plans"}[len(v.list) == 1])}
	if v.err != nil {
		st.LastError = termtext.New(v.err.Error())
	}
	return st
}

// Update implements ui.Screen.
func (v *Plans) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case plansMsg:
		v.list, v.err, v.loaded = msg.list, msg.err, msg.at
		v.cursor = max(min(v.cursor, len(v.list)-1), 0)
	case ui.ChangedMsg:
		return v, v.load()
	case reappliedMsg:
		return v, v.reapplied(msg)
	case ui.ScrollMsg:
		v.cursor = max(min(v.cursor+msg.Delta, len(v.list)-1), 0)
	case ui.ClickMsg:
		if row := msg.Y - plansTop; row >= 0 && row < len(v.list) {
			v.cursor = row
		}
	case tea.KeyPressMsg:
		return v, v.key(msg)
	}
	return v, nil
}

func (v *Plans) key(msg tea.KeyPressMsg) tea.Cmd {
	deleting := v.deleting
	v.deleting = false
	switch {
	case key.Matches(msg, keys.Get(keys.Down)):
		v.cursor = min(v.cursor+1, len(v.list)-1)
	case key.Matches(msg, keys.Get(keys.Up)):
		v.cursor = max(v.cursor-1, 0)
	case key.Matches(msg, keys.Get(keys.Refresh)):
		return v.load()
	case len(v.list) == 0:
	case key.Matches(msg, keys.Get(keys.Discard)):
		if !deleting {
			v.deleting, v.message = true, "Press d again to delete this saved plan."
			return nil
		}
		v.message = "Deleted."
		if err := v.s.store.Delete(v.list[v.cursor].SHA256); err != nil {
			v.message = err.Error()
		}
		return v.load()
	case key.Matches(msg, keys.Get(keys.Select)):
		return v.apply(v.list[v.cursor])
	}
	v.message = ""
	return nil
}

// apply builds the saved plan again; reapplied opens its preview, or
// says what changed.
func (v *Plans) apply(e mutate.Entry) tea.Cmd {
	if e.Err != nil {
		v.message = e.Err.Error()
		return nil
	}
	if e.State.State == "applied" {
		v.message = "This was done at " + e.State.Time + ". A saved plan can be used once."
		return nil
	}
	v.message = "Checking the system as it is now…"
	rebuild := v.s.rebuild
	return func() tea.Msg {
		p, err := mutate.Reapply(context.Background(), e.SHA256, e.Saved, rebuild)
		return reappliedMsg{sha: e.SHA256, plan: p, err: err}
	}
}

func (v *Plans) reapplied(msg reappliedMsg) tea.Cmd {
	if msg.err != nil {
		v.message = "Not applied: " + msg.err.Error()
		_ = v.s.store.Mark(msg.sha, "refused", msg.err.Error())
		return v.load()
	}
	v.message = ""
	sha, rebuild, s := msg.sha, v.s.rebuild, v.s
	sv := v.list[v.cursor].Saved
	// Planning again from the preview applies the same saved plan.
	again := func(ctx context.Context) (*mutate.Plan, error) { return mutate.Reapply(ctx, sha, sv, rebuild) }
	c := NewConfirm(msg.plan, savedRunner{s}, again, s.now)
	return open(c)
}

// plansTop is the first list row in the tab's coordinates.
const plansTop = 1

// Render implements ui.Screen.
func (v *Plans) Render(d ui.Display, width, height int) string {
	muted := d.Palette.Style(theme.Muted)
	if len(v.list) == 0 {
		lines := []string{"No saved plans.", ""}
		lines = append(lines, wrapStyled(muted, "A saved plan is a change you have looked at and want to make later. "+
			"To save one: go to Datasets (2), Snapshots (3) or Volumes (4), select something, press a and choose a change; "+
			"in the window that shows what will happen, press ctrl+s. It appears here, and enter makes it.", width)...)
		if v.err != nil {
			lines = append(lines, d.Palette.Style(theme.Error).Render(fitW(v.err.Error(), width)))
		}
		return strings.Join(lines, "\n")
	}
	lines := []string{muted.Render(fitW(fmt.Sprintf("%-17s %-8s %-12s %s", "SAVED (UTC)", "STATE", "SHA-256", "CHANGE"), width))}
	for i, e := range v.list {
		row := fmt.Sprintf("%-17s %-8s %-12s %s", savedTime(e.Saved.Created), e.State.State, e.SHA256[:12],
			termtext.New(e.Saved.Summary).Display())
		if e.Err != nil {
			row = fmt.Sprintf("%-17s %-8s %-12s %s", "?", "damaged", e.SHA256[:12], safe(e.Err.Error()))
		}
		row = fitW(row, width-2)
		if i == v.cursor {
			row = d.Palette.Style(theme.Accent).Bold(true).Render("> " + row)
		} else {
			row = "  " + row
		}
		lines = append(lines, row)
	}
	lines = append(lines, "")
	lines = append(lines, v.detail(d, width)...)
	if v.message != "" {
		lines = append(lines, "")
		lines = append(lines, wrapStyled(d.Palette.Style(theme.Warn), safe(v.message), width)...)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// detail shows the selected plan in full: the SHA-256 to name it by,
// who made it where, and the commands it had when saved.
func (v *Plans) detail(d ui.Display, width int) []string {
	e := v.list[v.cursor]
	sv := e.Saved
	muted := d.Palette.Style(theme.Muted)
	field := func(label, value string) string {
		return fitW(muted.Render(fmt.Sprintf("%-9s", label))+value, width)
	}
	who := fmt.Sprintf("uid %d", sv.UID)
	if sv.SudoUser != "" {
		who = termtext.New(sv.SudoUser).Display() + " (sudo)"
	}
	out := []string{
		lipgloss.NewStyle().Bold(true).Render(fitW(termtext.New(sv.Summary).Display(), width)),
		"",
		field("Saved", savedTime(sv.Created)+" UTC by "+who+" on "+termtext.New(sv.Host).Display()),
		field("State", e.State.State),
		field("SHA-256", e.SHA256),
	}
	if e.State.Reason != "" {
		out = append(out, wrapStyled(d.Palette.Style(theme.Warn), termtext.New(e.State.Reason).Display(), width)...)
	}
	label := "Command"
	if len(sv.Steps) > 1 {
		label = "Commands"
	}
	for i, st := range sv.Steps {
		if i > 0 {
			label = ""
		}
		out = append(out, field(label, termtext.New(strings.Join(st, " ")).Display()))
	}
	return append(out, "", muted.Render(fitW("Applying it checks the system first and works out the command again.", width)))
}

func savedTime(rfc string) string {
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return "?"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func fitW(s string, w int) string { return fit(s, max(w, 1)) }

// wrapStyled wraps s to width w, styling each line.
func wrapStyled(st lipgloss.Style, s string, w int) []string {
	var out []string
	for _, l := range strings.Split(ansi.Wrap(s, max(w, 1), " ;,"), "\n") {
		out = append(out, st.Render(l))
	}
	return out
}

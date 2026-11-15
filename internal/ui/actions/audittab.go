// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nuclear-gumbo/fstui/internal/audit"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// auditShown is how many records the view reads from the end of the log.
const auditShown = 2000

// Audit lists the audit log, newest first. It only reads; the log is never
// changed from fstui.
type Audit struct {
	s       *Session
	recs    []audit.Record
	skipped int
	err     error
	loaded  time.Time
	cursor  int
	filter  string
	typing  bool
}

// NewAudit returns the audit tab of a session.
func NewAudit(s *Session) *Audit { return &Audit{s: s} }

type auditMsg struct {
	recs    []audit.Record
	skipped int
	err     error
	at      time.Time
}

func (a *Audit) load() tea.Cmd {
	dir, now := a.s.paths.AuditDir, a.s.now
	return func() tea.Msg {
		recs, skipped, err := audit.Recent(dir, auditShown)
		return auditMsg{recs: recs, skipped: skipped, err: err, at: now()}
	}
}

// Keys lists the tab's bindings.
func (a *Audit) Keys() []key.Binding {
	if a.typing {
		return []key.Binding{keyEnterDone, keyEscClear}
	}
	return []key.Binding{keys.Get(keys.Filter), keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"), keys.Get(keys.Refresh)}
}

var (
	keyEnterDone = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "keep filter"))
	keyEscClear  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter"))
)

// Status reports the tab's data age and a count.
func (a *Audit) Status() ui.Status {
	st := ui.Status{Refreshed: a.loaded, Figure: fmt.Sprintf("%d audit records", len(a.shown()))}
	if a.err != nil {
		st.LastError = termtext.New(a.err.Error())
	}
	return st
}

// Update handles the tab's messages.
func (a *Audit) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case auditMsg:
		a.recs, a.skipped, a.err, a.loaded = msg.recs, msg.skipped, msg.err, msg.at
		a.cursor = max(min(a.cursor, len(a.shown())-1), 0)
	case ui.ChangedMsg:
		return a.load()
	case ui.ScrollMsg:
		a.cursor = max(min(a.cursor+msg.Delta, len(a.shown())-1), 0)
	case ui.ClickMsg:
		if row := msg.Y - auditTop; row >= 0 && row < len(a.shown()) {
			a.cursor = row
		}
	case tea.KeyPressMsg:
		return a.key(msg)
	}
	return nil
}

func (a *Audit) key(msg tea.KeyPressMsg) tea.Cmd {
	if a.typing {
		switch k := msg.String(); {
		case k == "esc":
			a.filter, a.typing = "", false
		case k == "enter":
			a.typing = false
		case k == "backspace" && a.filter != "":
			a.filter = a.filter[:len(a.filter)-1]
		case msg.Text != "" && len(a.filter) < maxField:
			a.filter += msg.Text
		}
		a.cursor = 0
		return nil
	}
	n := len(a.shown())
	switch {
	case key.Matches(msg, keys.Get(keys.Filter)):
		a.typing = true
	case key.Matches(msg, keys.Get(keys.Down)):
		a.cursor = min(a.cursor+1, n-1)
	case key.Matches(msg, keys.Get(keys.Up)):
		a.cursor = max(a.cursor-1, 0)
	case key.Matches(msg, keys.Get(keys.Refresh)):
		return a.load()
	}
	return nil
}

// line is a record as one row; the filter matches it.
func line(r audit.Record) string {
	t := r.Time
	if tt, err := time.Parse(time.RFC3339, r.Time); err == nil {
		t = tt.UTC().Format("2006-01-02 15:04:05")
	}
	result := "     "
	if r.ExitCode != nil {
		result = fmt.Sprintf("exit %d", *r.ExitCode)
	}
	return fmt.Sprintf("%-19s %-7s %-7s %-16s %-7s %s", t, r.Mode, r.Phase, r.Op, result, strings.Join(r.Argv, " "))
}

func (a *Audit) shown() []audit.Record {
	if a.filter == "" {
		return a.recs
	}
	var out []audit.Record
	for _, r := range a.recs {
		if strings.Contains(line(r), a.filter) {
			out = append(out, r)
		}
	}
	return out
}

// auditTop is the first record row in the tab's coordinates.
const auditTop = 2

// Render draws the tab in width x height.
func (a *Audit) Render(d ui.Display, width, height int) string {
	muted := d.Palette.Style(theme.Muted)
	head := fmt.Sprintf("%s, newest first", a.s.paths.AuditDir+"/"+audit.DefaultFile)
	if a.filter != "" || a.typing {
		head = "filter: " + termtext.New(a.filter).Display()
		if a.typing {
			head += ui.Cursor(d)
		}
	}
	if a.skipped > 0 {
		head += fmt.Sprintf("; %d damaged %s skipped", a.skipped, plural(a.skipped, "line", "lines"))
	}
	lines := []string{muted.Render(fitW(head, width)), muted.Render(fitW(fmt.Sprintf("%-19s %-7s %-7s %-16s %-7s %s", "TIME (UTC)", "MODE", "PHASE", "CHANGE", "RESULT", "COMMAND"), width))}
	recs := a.shown()
	if len(recs) == 0 {
		return strings.Join(append(lines, "No records."), "\n")
	}
	detail := a.detail(d, recs[a.cursor], width)
	rows := max(height-len(lines)-len(detail)-1, 1)
	start := max(min(a.cursor-rows/2, len(recs)-rows), 0)
	for i := start; i < min(start+rows, len(recs)); i++ {
		row := fitW(termtext.New(line(recs[i])).Display(), width-2)
		if i == a.cursor {
			row = d.Palette.Style(theme.Accent).Bold(true).Render("> " + row)
		} else {
			if recs[i].ExitCode != nil && *recs[i].ExitCode != 0 {
				row = d.Palette.Style(theme.Error).Render(row)
			}
			row = "  " + row
		}
		lines = append(lines, row)
	}
	return strings.Join(append(append(lines, ""), detail...), "\n")
}

func (a *Audit) detail(d ui.Display, r audit.Record, width int) []string {
	muted := d.Palette.Style(theme.Muted)
	who := fmt.Sprintf("uid %d", r.UID)
	if r.SudoUser != "" {
		who += " (sudo from " + termtext.New(r.SudoUser).Display() + ")"
	}
	out := []string{
		lipgloss.NewStyle().Bold(true).Render(fitW(termtext.New(strings.Join(r.Argv, " ")).Display(), width)),
		muted.Render(fitW(fmt.Sprintf("%s, plan %s step %d, %s", r.Time, r.PlanID, r.Step, who), width)),
	}
	if r.PlanSHA256 != "" {
		out = append(out, muted.Render(fitW("from saved plan "+r.PlanSHA256, width)))
	}
	if r.DurationMS != nil {
		out = append(out, muted.Render(fmt.Sprintf("took %d ms", *r.DurationMS)))
	}
	if r.Stderr != "" {
		out = append(out, wrapStyled(d.Palette.Style(theme.Error), termtext.New(r.Stderr).Display(), width)...)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

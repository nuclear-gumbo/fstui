// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nuclear-gumbo/fstui/internal/audit"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

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
	start   int // the first record shown, for clicks
}

// NewAudit returns the audit tab of a session.
func NewAudit(s *Session) *Audit { return &Audit{s: s} }

type auditMsg struct {
	recs    []audit.Record
	skipped int
	err     error
	at      time.Time
}

// defaultAuditEntries is limits.audit_entries when none was given.
const defaultAuditEntries = 2000

func (a *Audit) load() tea.Cmd {
	dir, now, n := a.s.paths.AuditDir, a.s.now, cmp.Or(a.s.auditEntries.Load(), defaultAuditEntries)
	return func() tea.Msg {
		recs, skipped, err := audit.Recent(dir, int(n))
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
	case ui.ConfigMsg:
		a.s.auditEntries.Store(int64(msg.Config.Limits.AuditEntries))
	case auditMsg:
		a.recs, a.skipped, a.err, a.loaded = msg.recs, msg.skipped, msg.err, msg.at
		a.cursor = max(min(a.cursor, len(a.shown())-1), 0)
	case ui.ChangedMsg:
		return a.load()
	case ui.ScrollMsg:
		a.cursor = max(min(a.cursor+msg.Delta, len(a.shown())-1), 0)
	case ui.ClickMsg:
		if row := a.start + msg.Y - auditTop; msg.Y >= auditTop && row < len(a.shown()) {
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

// auditCols are the table's titles.
var auditCols = []string{"TIME (UTC)", "MODE", "PHASE", "CHANGE", "RESULT", "COMMAND"}

// cells are a record as one row of the table.
func cells(r audit.Record) []string {
	t := r.Time
	if tt, err := time.Parse(time.RFC3339, r.Time); err == nil {
		t = tt.UTC().Format("2006-01-02 15:04:05")
	}
	return []string{t, r.Mode, r.Phase, r.Op, result(r), termtext.New(strings.Join(r.Argv, " ")).Display()}
}

// result is how a step ended: success, failed, or n/a for the record
// written before it ran.
func result(r audit.Record) string {
	switch {
	case r.ExitCode == nil:
		return "n/a"
	case *r.ExitCode == 0:
		return "success"
	}
	return "failed"
}

// line is a record as the filter matches it.
func line(r audit.Record) string { return strings.Join(cells(r), " ") }

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

// auditTop is the first record row in the tab's coordinates: under the
// heading, the table's top edge, titles and rule.
const auditTop = 1 + ui.GridChrome - 1

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
		head += fmt.Sprintf("; %d damaged %s skipped", a.skipped, format.Plural(a.skipped, "line", "lines"))
	}
	lines := []string{muted.Render(fitW(head, width))}
	recs := a.shown()
	if len(recs) == 0 {
		return strings.Join(append(lines, "No records."), "\n")
	}
	detail := a.detail(d, recs[a.cursor], width-2)
	box := ui.Panel{Title: "record"}.Render(d, strings.Join(detail, "\n"), width, len(detail)+2)
	rows := max(height-len(lines)-ui.GridChrome-len(detail)-2, 1)
	a.start = max(min(a.cursor-rows/2, len(recs)-rows), 0)
	g := ui.Grid{Cols: auditCols, Rows: make([][]string, len(recs)), Cursor: a.cursor, Style: func(i int) (lipgloss.Style, bool) {
		return d.Palette.Style(theme.Error), result(recs[i]) == "failed"
	}}
	for i, r := range recs {
		g.Rows[i] = cells(r)
	}
	lines = append(lines, g.Render(d, width, a.start, a.start+rows)...)
	return strings.Join(append(lines, box), "\n")
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
	if r.ExitCode != nil {
		out = append(out, muted.Render(fmt.Sprintf("%s: exit status %d", result(r), *r.ExitCode)))
	}
	if r.DurationMS != nil {
		out = append(out, muted.Render(fmt.Sprintf("took %d ms", *r.DurationMS)))
	}
	if r.Stderr != "" {
		out = append(out, wrapStyled(d.Palette.Style(theme.Error), termtext.New(r.Stderr).Display(), width)...)
	}
	return out
}

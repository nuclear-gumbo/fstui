// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"context"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/format"
	"github.com/nuclear-gumbo/fstui/internal/schedule"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Schedules is the Config view's Schedules tab: what runs scrubs, trims,
// fstui check and fstui watch, when, and through what. The changes
// (a) are the write module's, as plans.
type Schedules struct {
	read    func(context.Context) ([]schedule.Job, error) // nil: not available in this build
	jobs    []schedule.Job
	err     string
	loading bool
	loaded  bool
	cursor  int
	// cannot says why schedules cannot be changed here; empty when they can.
	cannot  string
	now     func() time.Time
	timeout time.Duration // limits.read_timeout
}

type schedulesMsg struct {
	jobs []schedule.Job
	err  error
}

// NewSchedules returns the tab reading with read; cannot says why
// changes are not offered, or is empty when they are.
func NewSchedules(read func(context.Context) ([]schedule.Job, error), cannot string, timeout time.Duration, now func() time.Time) *Schedules {
	return &Schedules{read: read, cannot: cannot, timeout: timeout, now: now}
}

// load reads the schedules in the background.
func (s *Schedules) load() tea.Cmd {
	if s.read == nil || s.loading {
		return nil
	}
	s.loading = true
	read, timeout := s.read, s.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		jobs, err := read(ctx)
		return schedulesMsg{jobs, err}
	}
}

// Selection is the selected schedule, when it is one fstui can change.
func (s *Schedules) Selection() (Target, bool) {
	if s.cannot != "" || s.cursor >= len(s.jobs) || s.jobs[s.cursor].ID == "" {
		return Target{}, false
	}
	return Target{Backend: "system", Kind: "schedule", Names: []string{s.jobs[s.cursor].ID}}, true
}

// noChangeReason says why the selected schedule offers no changes.
func (s *Schedules) noChangeReason() string {
	if s.cannot != "" {
		return s.cannot
	}
	if s.cursor >= len(s.jobs) {
		return ""
	}
	switch j := s.jobs[s.cursor]; {
	case j.ID == "":
		return j.What + " (" + j.By + ") was not written by fstui, so it is not changed here"
	case j.Choices == nil:
		return j.What + " cannot be scheduled here: " + j.Note
	}
	return ""
}

// Keys are the tab's bindings.
func (s *Schedules) Keys() []key.Binding {
	return []key.Binding{keys.As(keys.Refresh, "read again"), keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"), keys.Get(keys.First), keys.Get(keys.Last)}
}

// Update handles keys, the schedules read, and changes made.
func (s *Schedules) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case schedulesMsg:
		s.loading, s.loaded = false, true
		s.err = ""
		if msg.err != nil {
			s.err = msg.err.Error()
			return nil
		}
		s.jobs = msg.jobs
		s.cursor = min(s.cursor, max(len(s.jobs)-1, 0))
	case ConfigMsg:
		s.timeout = msg.Config.Limits.ReadTimeout
	case ChangedMsg:
		if s.loaded {
			return s.load()
		}
	case ScrollMsg:
		s.cursor = max(min(s.cursor+msg.Delta, len(s.jobs)-1), 0)
	case ClickMsg:
		if i := msg.Y - s.top(); i >= 0 && i < len(s.jobs) {
			s.cursor = i
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Get(keys.Down)):
			s.cursor = min(s.cursor+1, max(len(s.jobs)-1, 0))
		case key.Matches(msg, keys.Get(keys.Up)):
			s.cursor = max(s.cursor-1, 0)
		case key.Matches(msg, keys.Get(keys.First)):
			s.cursor = 0
		case key.Matches(msg, keys.Get(keys.Last)):
			s.cursor = max(len(s.jobs)-1, 0)
		case key.Matches(msg, keys.Get(keys.Refresh)):
			return s.load()
		}
	}
	return nil
}

// top is the line of the first row, below the intro and the header.
func (s *Schedules) top() int { return 3 }

// Render draws the tab.
func (s *Schedules) Render(d Display, width, _ int) string {
	return styleKeys(d, s.render(d, width))
}

func (s *Schedules) render(d Display, width int) string {
	muted := d.Palette.Style(theme.Muted)
	lines := []string{ansi.Truncate("Scrubs, trims and notifications on a schedule, and what runs them.", width, "…"), ""}
	switch {
	case s.read == nil:
		return strings.Join(append(lines, "Schedules are not available in this build."), "\n")
	case s.err != "":
		lines = append(lines, d.Palette.Style(theme.Error).Render(ansi.Wrap("Could not read the schedules: "+s.err, width, "")))
		return strings.Join(lines, "\n")
	case !s.loaded:
		return strings.Join(append(lines, muted.Render("Reading…")), "\n")
	}
	lines = append(lines, s.table(d, width)...)
	if s.cursor < len(s.jobs) {
		j := s.jobs[s.cursor]
		lines = append(lines, "")
		switch {
		case j.ID == "":
			lines = append(lines, ansi.Wrap("Not written by fstui, so not changed here.", width, ""))
		case j.Choices == nil:
			lines = append(lines, ansi.Wrap("Cannot be scheduled here: "+j.Note+".", width, ""))
		case s.cannot != "":
			lines = append(lines, ansi.Wrap(s.cannot+".", width, ""))
		default:
			lines = append(lines, ansi.Wrap(keyName(keys.Actions)+" changes it: "+strings.Join(j.Choices, ", ")+".", width, ""))
		}
		if j.Note != "" && j.Choices != nil {
			lines = append(lines, ansi.Wrap(j.Note+".", width, ""))
		}
	}
	return strings.Join(lines, "\n")
}

// table lists the schedules: what, when, next run, and what runs it.
func (s *Schedules) table(d Display, width int) []string {
	pal := d.Palette
	whatCol, whenCol, nextCol := len("WHAT"), len("WHEN"), len("NEXT")
	for _, j := range s.jobs {
		whatCol, whenCol, nextCol = max(whatCol, len(j.What)), max(whenCol, len(j.When)), max(nextCol, len(s.next(j)))
	}
	byCol := max(width-2-whatCol-2-whenCol-2-nextCol-2, len("BY"))
	out := []string{pal.Style(theme.Muted).Render("  " + pad(whatCol, "WHAT") + "  " + pad(whenCol, "WHEN") + "  " + pad(nextCol, "NEXT") + "  BY")}
	for i, j := range s.jobs {
		marker := "  "
		if i == s.cursor {
			marker = pal.Style(theme.Accent).Render(">") + " "
		}
		when := pad(whenCol, j.When)
		if j.When == schedule.Off {
			when = pal.Style(theme.Muted).Render(when)
		}
		out = append(out, marker+pad(whatCol, j.What)+"  "+when+"  "+pad(nextCol, s.next(j))+"  "+ansi.Truncate(j.By, byCol, "…"))
	}
	return out
}

// next is when a schedule runs next, as "in 3d".
func (s *Schedules) next(j schedule.Job) string {
	if j.Next.IsZero() {
		return ""
	}
	return "in " + format.Age(j.Next.Sub(s.now()))
}

// selectJob moves the cursor to the job with id, and reports whether it
// is listed.
func (s *Schedules) selectJob(id string) bool {
	i := slices.IndexFunc(s.jobs, func(j schedule.Job) bool { return j.ID == id })
	if i < 0 {
		return false
	}
	s.cursor = i
	return true
}

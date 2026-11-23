// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// hostsPanel is the dashboard's Hosts panel: a line per host, read with
// the host list's lister every refresh.hosts while the dashboard is
// shown, so a screen left on all day watches a whole cluster.
type hostsPanel struct {
	lister  *ui.HostLister
	entries []ui.HostEntry
	lines   map[string]string
	gen     int // a read or tick of an earlier activation is dropped
	active  bool
}

type hostsReadMsg struct {
	gen   int
	name  string
	text  string
	found []ui.HostEntry
}

type hostsTickMsg struct{ gen int }

// activate starts reading, at once and then every every.
func (h *hostsPanel) activate(every time.Duration) tea.Cmd {
	if h.lister == nil {
		return nil
	}
	h.active = true
	h.gen++
	if h.entries == nil {
		h.entries, h.lines = h.lister.Entries(), map[string]string{}
	}
	return tea.Batch(h.read(h.entries), h.tick(every))
}

func (h *hostsPanel) deactivate() { h.active = false }

func (h *hostsPanel) tick(every time.Duration) tea.Cmd {
	gen := h.gen
	return tea.Tick(every, func(time.Time) tea.Msg { return hostsTickMsg{gen: gen} })
}

func (h *hostsPanel) read(es []ui.HostEntry) tea.Cmd {
	gen, cmds := h.gen, make([]tea.Cmd, 0, len(es))
	for _, e := range es {
		cmds = append(cmds, func() tea.Msg {
			text, found := h.lister.Status(context.Background(), e)
			return hostsReadMsg{gen: gen, name: e.Name, text: text, found: found}
		})
	}
	return tea.Batch(cmds...)
}

// update takes the panel's own messages; others are not its.
func (h *hostsPanel) update(msg tea.Msg, every time.Duration) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case hostsReadMsg:
		if msg.gen != h.gen {
			return nil, true
		}
		h.lines[msg.name] = msg.text
		var added []ui.HostEntry
		for _, f := range msg.found {
			if !slices.ContainsFunc(h.entries, func(e ui.HostEntry) bool { return e.Name == f.Name }) {
				h.entries = append(h.entries, f)
				added = append(added, f)
			}
		}
		return h.read(added), true
	case hostsTickMsg:
		if msg.gen != h.gen || !h.active {
			return nil, true
		}
		return tea.Batch(h.read(h.entries), h.tick(every)), true
	}
	return nil, false
}

func (h *hostsPanel) render(d ui.Display, width int) string {
	if h.lister == nil {
		return d.Palette.Style(theme.Muted).Render("No other hosts: this build leaves out the remote module.")
	}
	muted := d.Palette.Style(theme.Muted)
	nameCol := 0
	for _, e := range h.entries {
		nameCol = max(nameCol, len(e.Name))
	}
	lines := make([]string, 0, len(h.entries))
	for _, e := range h.entries {
		text, ok := h.lines[e.Name]
		if !ok {
			text = "reading…"
		}
		if e.Cluster != "" {
			text = termtext.New(e.Cluster).Display() + ": " + text
		}
		name := e.Name + strings.Repeat(" ", nameCol-len(e.Name))
		lines = append(lines, ansi.Truncate(name+"  "+muted.Render(termtext.New(text).Display()), width, "…"))
	}
	return strings.Join(lines, "\n")
}

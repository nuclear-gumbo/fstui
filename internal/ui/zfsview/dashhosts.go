// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
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

// activate starts reading, at once and then every refresh.hosts, the
// hosts of cfg.
func (h *hostsPanel) activate(cfg config.Config) tea.Cmd {
	if h.lister == nil {
		return nil
	}
	h.active = true
	h.gen++
	h.entries, h.lines = h.lister.Entries(cfg), map[string]string{}
	return tea.Batch(h.read(h.entries, cfg), h.tick(cfg.Refresh.Hosts))
}

func (h *hostsPanel) deactivate() { h.active = false }

func (h *hostsPanel) tick(every time.Duration) tea.Cmd {
	gen := h.gen
	return tea.Tick(every, func(time.Time) tea.Msg { return hostsTickMsg{gen: gen} })
}

// read reads es with cfg, a copy: the reads run outside the UI
// goroutine.
func (h *hostsPanel) read(es []ui.HostEntry, cfg config.Config) tea.Cmd {
	gen, cmds := h.gen, make([]tea.Cmd, 0, len(es))
	for _, e := range es {
		cmds = append(cmds, func() tea.Msg {
			text, found := h.lister.Status(context.Background(), cfg, e)
			return hostsReadMsg{gen: gen, name: e.Name, text: text, found: found}
		})
	}
	return tea.Batch(cmds...)
}

// update takes the panel's own messages; others are not its.
func (h *hostsPanel) update(msg tea.Msg, cfg config.Config) (tea.Cmd, bool) {
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
		return h.read(added, cfg), true
	case hostsTickMsg:
		if msg.gen != h.gen || !h.active {
			return nil, true
		}
		h.entries = mergeEntries(h.lister.Entries(cfg), h.entries)
		return tea.Batch(h.read(h.entries, cfg), h.tick(cfg.Refresh.Hosts)), true
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

// mergeEntries is the hosts known now, with the nodes found by reading
// kept: a host saved in the Config view joins the panel at its next
// read, one removed leaves it.
func mergeEntries(known, read []ui.HostEntry) []ui.HostEntry {
	out := slices.Clone(known)
	for _, e := range read {
		if e.Cluster != "" && !slices.ContainsFunc(out, func(k ui.HostEntry) bool { return k.Name == e.Name }) {
			out = append(out, e)
		}
	}
	return out
}

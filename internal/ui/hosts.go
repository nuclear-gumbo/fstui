// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"context"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// HostEntry is one line of the host list.
type HostEntry struct {
	Name        string // what --host takes; the machine fstui runs on is "local"
	Destination string // where ssh connects; empty for this machine
	Cluster     string // the Proxmox VE cluster it was found in, or ""
	Current     bool   // the host fstui shows now
}

// HostLister fills the host list: the hosts known without connecting,
// and then, a host at a time, what each holds. Both take the settings
// as they are now, so a host saved in the Config view is listed at once.
type HostLister struct {
	Entries func(cfg config.Config) []HostEntry
	// Status reads one host: a line on its pools ("3 pools, 1 DEGRADED")
	// or why it could not be read, and the other nodes of its Proxmox
	// VE cluster, if it is in one. It runs outside the UI goroutine.
	Status func(ctx context.Context, cfg config.Config, e HostEntry) (string, []HostEntry)
}

// SwitchHostMsg asks fstui to show another host: the program ends and
// starts again on it (see Model.Next).
type SwitchHostMsg struct{ Host HostEntry }

type hostStatusMsg struct {
	list  *hostList
	name  string
	text  string
	found []HostEntry
}

// hostList is the modal H opens.
type hostList struct {
	lister  HostLister
	cfg     config.Config
	entries []HostEntry
	status  map[string]string
	cursor  int
}

func openHosts(l HostLister, cfg config.Config) (Modal, tea.Cmd) {
	h := &hostList{lister: l, cfg: cfg, entries: l.Entries(cfg), status: map[string]string{}}
	return h, h.read(h.entries)
}

// read asks each host for its status at the same time.
func (h *hostList) read(es []HostEntry) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(es))
	cfg := h.cfg
	for _, e := range es {
		cmds = append(cmds, func() tea.Msg {
			text, found := h.lister.Status(context.Background(), cfg, e)
			return hostStatusMsg{list: h, name: e.Name, text: text, found: found}
		})
	}
	return tea.Batch(cmds...)
}

func (h *hostList) Busy() bool { return false }

func (h *hostList) Keys() []key.Binding {
	return []key.Binding{keys.As(keys.Select, "show this host"), keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"), keys.As(keys.Back, "close")}
}

func (h *hostList) Update(msg tea.Msg) (Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case hostStatusMsg:
		if msg.list != h {
			return h, nil
		}
		h.status[msg.name] = msg.text
		var added []HostEntry
		for _, f := range msg.found {
			if !slices.ContainsFunc(h.entries, func(e HostEntry) bool { return e.Name == f.Name }) {
				h.entries = append(h.entries, f)
				added = append(added, f)
			}
		}
		h.sort()
		return h, h.read(added)
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Get(keys.Back), keys.Get(keys.Hosts)):
			return nil, nil
		case key.Matches(msg, keys.Get(keys.Down)):
			h.cursor = min(h.cursor+1, len(h.entries)-1)
		case key.Matches(msg, keys.Get(keys.Up)):
			h.cursor = max(h.cursor-1, 0)
		case key.Matches(msg, keys.Get(keys.Select)):
			if e := h.entries[h.cursor]; !e.Current {
				return nil, func() tea.Msg { return SwitchHostMsg{Host: e} }
			}
		}
	}
	return h, nil
}

// sort keeps this machine and the configured hosts first, in their
// order, then each cluster's nodes by name; the cursor stays on its
// host.
func (h *hostList) sort() {
	at := h.entries[h.cursor].Name
	slices.SortStableFunc(h.entries, func(a, b HostEntry) int {
		if c := strings.Compare(a.Cluster, b.Cluster); c != 0 || a.Cluster == "" {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	h.cursor = slices.IndexFunc(h.entries, func(e HostEntry) bool { return e.Name == at })
}

func (h *hostList) Render(d Display, width, height int) string {
	w := min(width, maxHostListWidth)
	iw, _ := Inner(w, height)
	muted, accent := d.Palette.Style(theme.Muted), d.Palette.Style(theme.Accent)
	nameCol := 0
	for _, e := range h.entries {
		nameCol = max(nameCol, len(e.Name))
	}
	var lines []string
	group := "\x00"
	for i, e := range h.entries {
		if e.Cluster != group {
			if group = e.Cluster; group != "" {
				lines = append(lines, "", muted.Render("Proxmox VE cluster "+termtext.New(group).Display()))
			}
		}
		marker := "  "
		if i == h.cursor {
			marker = accent.Render(">") + " "
		}
		status, ok := h.status[e.Name]
		if !ok {
			status = "reading…"
		}
		if e.Current {
			status = "shown now; " + status
		}
		line := marker + pad(nameCol, e.Name) + "  " + muted.Render(termtext.New(status).Display())
		lines = append(lines, fit(line, iw))
	}
	lines = append(lines, "", muted.Render(fit("A host is read over SSH, read-only; hosts: in config.yaml lists them.", iw)))
	return Panel{Title: "hosts", Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

// maxHostListWidth keeps the list readable on a wide terminal.
const maxHostListWidth = 100

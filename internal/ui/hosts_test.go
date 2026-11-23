// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// lister lists this machine and one configured host; pve1 turns out to
// be a node of the cluster lab, with pve2.
func lister() *HostLister {
	return &HostLister{
		Entries: func(config.Config) []HostEntry {
			return []HostEntry{{Name: "local", Current: true}, {Name: "pve1", Destination: "root@pve1"}}
		},
		Status: func(_ context.Context, _ config.Config, e HostEntry) (string, []HostEntry) {
			switch e.Name {
			case "pve1":
				return "2 pools", []HostEntry{{Name: "pve2", Destination: "root@pve2", Cluster: "lab"}, {Name: "pve3", Destination: "pve3", Cluster: "evil\x1b]0;owned\a\x1b[2J"}}
			case "pve2":
				return "could not be reached: ssh: connect to host pve2 port 22: No route to host", nil
			}
			return "1 pool", nil
		},
	}
}

// run feeds cmd's messages back to the model until none are left, as
// Bubble Tea would.
func run(m Model, cmd tea.Cmd) Model {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case nil:
		default:
			var next tea.Cmd
			out, next := m.Update(msg)
			m = out.(Model) //nolint:forcetypeassert // Update always returns Model
			queue = append(queue, next)
		}
	}
	return m
}

func TestHostList(t *testing.T) {
	s := Session{Host: termtext.New("dev"), Privilege: "root"}
	m := sized(New(s, nil, Options{Profile: colorprofile.ASCII, Now: func() time.Time { return epoch }, Hosts: lister()}), 100, 30)
	m, cmd := press(t, m, "H")
	m = run(m, cmd)
	if raw := m.View().Content; strings.Contains(raw, "\x1b]0;") || strings.Contains(raw, "\x1b[2J") {
		t.Error("a cluster name reached the terminal as an escape sequence")
	}
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"local", "shown now; 1 pool", "pve1   2 pools", "Proxmox VE cluster lab", "pve2", "could not be reached"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// enter on the host shown now does nothing; on another, it ends the
	// program with that host as the next.
	enter := func(m Model) (Model, tea.Cmd) {
		out, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		return out.(Model), cmd //nolint:forcetypeassert // Update always returns Model
	}
	m, cmd = enter(m)
	if cmd != nil {
		t.Error("enter on the current host did something")
	}
	for i := 0; m.modal.(*hostList).entries[m.modal.(*hostList).cursor].Name != "pve2"; i++ { //nolint:forcetypeassert // the list is open
		if i > 5 {
			t.Fatal("pve2 not in the list")
		}
		m, _ = press(t, m, "j")
	}
	_, cmd = enter(m)
	if cmd == nil {
		t.Fatal("enter on pve2 did nothing")
	}
	msg := cmd()
	sw, ok := msg.(SwitchHostMsg)
	if !ok || sw.Host.Name != "pve2" || sw.Host.Destination != "root@pve2" {
		t.Fatalf("enter gave %#v", msg)
	}
	out2, quit := m.Update(sw)
	if next, ok := out2.(Model).Next(); !ok || next.Name != "pve2" || quit == nil { //nolint:forcetypeassert // Update always returns Model
		t.Errorf("next %v %v, quit %v", next, ok, quit)
	}
}

// Without the remote module there is no list: H does nothing.
func TestHostListAbsent(t *testing.T) {
	m := sized(newModel(colorprofile.ASCII), 80, 24)
	if m, _ = press(t, m, "H"); m.modal != nil {
		t.Error("H opened something")
	}
}

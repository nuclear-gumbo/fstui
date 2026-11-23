// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// drain runs cmd's messages through the dashboard, as Bubble Tea would,
// leaving out ticks: they would wait refresh.hosts.
func drain(d *Dashboard, cmd tea.Cmd) {
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
		case hostsReadMsg:
			_, next := d.Update(msg)
			queue = append(queue, next)
		}
	}
}

// The hosts panel reads every host when the dashboard is shown, and the
// other nodes of a cluster one of them belongs to.
func TestDashboardHostsPanel(t *testing.T) {
	d, _, _ := dashboardOver(t, "hosts")
	d.opts.Settings.Refresh.Hosts = time.Millisecond // the loop below waits out each tick
	reads := 0
	d.hosts.lister = &ui.HostLister{
		Entries: func() []ui.HostEntry { return []ui.HostEntry{{Name: "local"}, {Name: "pve1", Destination: "pve1"}} },
		Status: func(_ context.Context, e ui.HostEntry) (string, []ui.HostEntry) {
			reads++
			if e.Name == "pve1" {
				return "2 pools, 1 DEGRADED", []ui.HostEntry{{Name: "pve2", Destination: "pve2", Cluster: "lab"}}
			}
			return "1 pool", nil
		},
	}
	if out := renderDash(d, 100, 20); !strings.Contains(out, "+-hosts") {
		t.Fatalf("no hosts panel:\n%s", out)
	}
	drain(d, d.Activate())
	out := renderDash(d, 100, 20)
	for _, want := range []string{"local  1 pool", "pve1   2 pools, 1 DEGRADED", "pve2   lab: "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if reads != 3 {
		t.Errorf("%d reads, want 3 (two hosts, then the node found)", reads)
	}
	// A tick from before the dashboard was hidden reads nothing.
	d.Deactivate()
	if _, cmd := d.Update(hostsTickMsg{gen: d.hosts.gen}); cmd != nil {
		t.Error("a hidden dashboard read the hosts")
	}
}

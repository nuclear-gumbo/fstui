// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_noremote && !fstui_nozfs && !fstui_demo

package main

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/pve"
)

// A cluster's nodes are listed without this node, the hosts already in
// config.yaml or a node named local, and at most maxNodes of them.
func TestNodes(t *testing.T) {
	m := pve.Members{Self: "pve1", Cluster: "lab", Nodes: []pve.Node{
		{Name: "pve1"}, {Name: "pve2", IP: netip.MustParseAddr("192.0.2.12")}, {Name: "nas"}, {Name: "local"},
	}}
	cfg := config.Default()
	cfg.Hosts = []config.Host{{Name: "nas", Destination: "root@nas"}}
	got := nodes(m, cfg, "root@", "pve2", true)
	if len(got) != 1 || got[0].Name != "pve2" || got[0].Destination != "root@192.0.2.12" || !got[0].Current || got[0].Cluster != "lab" {
		t.Errorf("%+v", got)
	}
	if got := nodes(m, cfg, "", "", false); len(got) != 1 || got[0].Destination != "pve2" {
		t.Errorf("by name: %+v", got)
	}
	big := pve.Members{Self: "a", Cluster: "big"}
	for i := range 1000 {
		big.Nodes = append(big.Nodes, pve.Node{Name: fmt.Sprintf("n%d", i)})
	}
	if got := nodes(big, cfg, "", "", false); len(got) != maxNodes {
		t.Errorf("%d nodes listed, want %d", len(got), maxNodes)
	}
}

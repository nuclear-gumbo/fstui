// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_noremote

package demo

import (
	"errors"
	"slices"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Each node of the demo cluster holds its own pools; the unreachable one
// fails as a real host does.
func TestHosts(t *testing.T) {
	m, err := Members()
	if err != nil || m.Cluster != "lab" || len(m.Nodes) != 4 {
		t.Fatalf("%+v %v", m, err)
	}
	for node, want := range nodePools {
		c, err := NewHost(node)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.poolNames(); !slices.Equal(got, want) {
			t.Errorf("%s: pools %v, want %v", node, got, want)
		}
	}
	if _, err := NewHost(UnreachableNode); !errors.Is(err, execute.ErrUnreachable) {
		t.Errorf("%s: %v", UnreachableNode, err)
	}
	if _, err := NewHost("nowhere"); !errors.Is(err, errNoNode) {
		t.Errorf("nowhere: %v", err)
	}
	for _, n := range m.Nodes {
		if _, ok := nodePools[n.Name]; !ok && n.Name != UnreachableNode {
			t.Errorf("node %s has no pools in the demo", n.Name)
		}
	}
}

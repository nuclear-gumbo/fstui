// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_noremote

package demo

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/nuclear-gumbo/fstui"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/pve"
)

// The demo's other hosts are the nodes of a recorded Proxmox VE
// cluster. Each holds some of the demo's pools, read through the same
// recordings; one cannot be reached, to show how fstui says so.
var nodePools = map[string][]string{
	"pve1":   {"tank", "fast"},
	"pve2":   {"backup", "archive"},
	"store1": {"media", "vault", "scratch"},
}

// UnreachableNode is the node the demo cannot reach.
const UnreachableNode = "pve4"

// Members is the recorded cluster.
func Members() (pve.Members, error) {
	b, err := fs.ReadFile(fstui.Recorded, fstui.RecordedMembers)
	if err != nil {
		return pve.Members{}, fmt.Errorf("demo cluster: %w", err)
	}
	return pve.ParseMembers(b)
}

// errNoNode is returned for a host the demo does not have.
var errNoNode = errors.New("the demo has no such host")

// NewHost is a node of the demo cluster: the demo's recordings with only
// its pools imported, the others taken out as an export takes them.
func NewHost(node string) (*Commands, error) {
	if node == UnreachableNode {
		return nil, unreachable(node)
	}
	keep, ok := nodePools[node]
	if !ok {
		return nil, fmt.Errorf("%s: %w", node, errNoNode)
	}
	c := New()
	if _, err := c.load(); err != nil {
		return nil, err
	}
	for _, p := range c.poolNames() {
		if !slices.Contains(keep, p) {
			if err := c.export(p); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

// unreachable is how ssh fails on a node that is down, worded as fstui
// reports it for a real host.
func unreachable(node string) error {
	return unreachableError(fmt.Sprintf("%s could not be reached: ssh: connect to host %s port 22: No route to host", node, node))
}

// unreachableError is execute.ErrUnreachable without its own words.
type unreachableError string

func (e unreachableError) Error() string        { return string(e) }
func (e unreachableError) Is(target error) bool { return target == execute.ErrUnreachable }

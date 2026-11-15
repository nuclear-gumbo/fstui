// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_nowrite

package main

import (
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/demo"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// The demo must never run zfs for a change: its write hooks always
// simulate, so the session uses a simulated mutator and the demo's own
// files (actions.DemoPaths).
func TestDemoChangesAreSimulated(t *testing.T) {
	cmds := demo.New()
	h := demoWrite(cmds, zfs.NewReader(cmds))
	if h.simulate == nil || h.offers == nil || h.tools != nil {
		t.Fatalf("demo write hooks = %+v", h)
	}
}

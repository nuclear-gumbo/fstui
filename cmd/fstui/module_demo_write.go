// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_nowrite

package main

import (
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"

	"github.com/nuclear-gumbo/fstui/internal/demo"
	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/ui/actions"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// demoWrite gives the demo the same changes as a real build, planned
// against its recordings and carried out on them in memory: nothing is
// run, and nothing outside the demo's own temporary files is written.
func demoWrite(cmds *demo.Commands, r *zfs.Reader, sched *demo.Schedules) writeHooks {
	// Pool creation lists the demo's made-up disks; it plans and previews
	// a new pool, and the demo does not carry it out.
	src := disks.Source{Run: cmds, PoolDevices: r.PoolDevices}
	b := plan.Builder{R: r, Devices: src.Devices}
	simulate := func(a execute.Argv, stdin []byte) (execute.Result, error) {
		if sched.Handles(a) {
			return sched.Apply(a, stdin)
		}
		return cmds.Apply(a, stdin)
	}
	return writeHooks{offers: actions.ZFSOffers(b, time.Now), rebuild: b.Build, simulate: simulate}
}

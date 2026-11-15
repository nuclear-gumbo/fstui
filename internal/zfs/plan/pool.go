// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Pool changes: scrub, trim, initialize, clear. They need root: zpool
// has no delegation. Each is blocked where zpool would refuse it, or,
// for pausing an idle pool, where zpool would silently do nothing
// (tested on 2.3.9).

// poolChange describes one of the pool changes.
type poolChange struct {
	summary string // with %s for the pool
	opts    []string
	outcome []string
	check   func(zfs.Activity) string // why it cannot be done now, or ""
}

var poolChanges = map[mutate.Op]poolChange{
	mutate.ScrubStart: {
		summary: "Scrub %s", opts: []string{"scrub"},
		outcome: []string{
			"every block in the pool is read and checked against its checksum, and repaired from a good copy where it can be",
			"other reads and writes are slower until it finishes; it can be paused",
		},
		check: func(a zfs.Activity) string {
			switch {
			case a.ScanState == "paused" && a.ScanKind == "SCRUB":
				return "a scrub is paused; resume it instead"
			case a.Scanning():
				return "a " + scanWord(a.ScanKind) + " is running; only one scan runs at a time"
			}
			return ""
		},
	},
	mutate.ScrubPause: {
		summary: "Pause the scrub of %s", opts: []string{"scrub", "-p"},
		outcome: []string{"the scrub stops where it is and can be resumed later from there"},
		check: func(a zfs.Activity) string {
			if a.ScanKind != "SCRUB" || a.ScanState != "running" {
				return "no scrub is running"
			}
			return ""
		},
	},
	mutate.ScrubResume: {
		summary: "Resume the scrub of %s", opts: []string{"scrub"},
		outcome: []string{"the paused scrub carries on from where it stopped"},
		check: func(a zfs.Activity) string {
			if a.ScanKind != "SCRUB" || a.ScanState != "paused" {
				return "no scrub is paused"
			}
			return ""
		},
	},
	mutate.ScrubStop: {
		summary: "Stop the scrub of %s", opts: []string{"scrub", "-s"},
		outcome: []string{"the scrub ends; a new one starts from the beginning"},
		check: func(a zfs.Activity) string {
			if a.ScanKind != "SCRUB" || !a.Scanning() {
				return "no scrub is running or paused"
			}
			return ""
		},
	},
	mutate.ErrorScrub: {
		summary: "Check the known errors of %s", opts: []string{"scrub", "-e"},
		outcome: []string{"only the blocks zpool status lists as damaged are read again; much faster than a full scrub"},
		check: func(a zfs.Activity) string {
			if a.Scanning() {
				return "a " + scanWord(a.ScanKind) + " is running"
			}
			return ""
		},
	},
	mutate.TrimStart: {
		summary: "Trim %s", opts: []string{"trim"},
		outcome: []string{"the devices are told which blocks are free, so SSDs and thin storage can reuse them"},
		check: func(a zfs.Activity) string {
			switch {
			case a.AnyDevice(func(d zfs.DeviceActivity) bool { return d.Trim == "ACTIVE" }):
				return "a trim is already running"
			case len(a.Devices) > 0 && !a.AnyDevice(func(d zfs.DeviceActivity) bool { return !d.TrimNotSupp }):
				return "none of the devices supports trim"
			}
			return ""
		},
	},
	mutate.InitializeStart: {
		summary: "Initialize %s", opts: []string{"initialize"},
		outcome: []string{
			"a pattern is written to all free space on every device, so later writes are not slowed by first-touch costs (thin disks, cloud volumes)",
			"other I/O is slower until it finishes",
		},
		check: func(a zfs.Activity) string {
			if a.AnyDevice(func(d zfs.DeviceActivity) bool { return d.Init == "ACTIVE" }) {
				return "initializing is already running"
			}
			return ""
		},
	},
	mutate.PoolClear: {
		summary: "Clear the errors of %s", opts: []string{"clear"},
		outcome: []string{
			"the read, write and checksum error counts go back to zero, and a device taken out for errors is tried again",
			"the list of damaged files stays until a scrub finds them repaired",
		},
		check: func(zfs.Activity) string { return "" },
	},
}

// PoolAvailability says, for each pool change, why it cannot be made
// now, from one read of the pool's activity; "" where it can.
func (b Builder) PoolAvailability(ctx context.Context, pool string) (map[mutate.Op]string, error) {
	if _, err := name.ParsePool(pool); err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	a, err := b.R.PoolActivity(ctx, pool)
	if err != nil {
		return nil, err
	}
	out := make(map[mutate.Op]string, len(poolChanges))
	for op, c := range poolChanges {
		out[op] = c.check(a)
		if b.Unprivileged {
			out[op] = "pool changes need root"
		}
	}
	return out, nil
}

func scanWord(kind string) string {
	switch kind {
	case "RESILVER":
		return "resilver"
	case "ERRORSCRUB":
		return "error check"
	}
	return "scrub"
}

// PoolChange plans one of the pool changes for pool.
func (b Builder) PoolChange(ctx context.Context, op mutate.Op, pool string) (*mutate.Plan, error) {
	c, ok := poolChanges[op]
	if !ok {
		return nil, fmt.Errorf("%s is not a pool change", op)
	}
	if _, err := name.ParsePool(pool); err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	p := newPlan(op, fmt.Sprintf(c.summary, show(pool)), []string{pool}, map[string]string{"pool": pool},
		execute.Argv{Tool: execute.Zpool, Options: c.opts, Operands: []string{pool}})
	affects(p, c.outcome...)
	if op == mutate.ScrubStop {
		p.Irreversible = []string{"The progress of this scrub is lost."}
	}
	blockIf(p, b.Unprivileged, "pool changes need root")
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		a, err := b.R.PoolActivity(ctx, pool)
		if err != nil {
			return nil, err
		}
		f := mutate.Fingerprint{pool + " (pool guid)": a.GUID, pool + " (scan)": a.ScanKind + " " + a.ScanState}
		for _, d := range a.Devices {
			f[pool+" "+d.Name+" (initialize, trim)"] = d.Init + " " + d.Trim
		}
		return f, nil
	}
	a, err := b.R.PoolActivity(ctx, pool)
	if err != nil {
		return nil, err
	}
	if why := c.check(a); why != "" {
		p.Blocked = append(p.Blocked, why)
	}
	f, err := read(ctx)
	if err != nil {
		return nil, err
	}
	p.Baseline, p.Recheck = f, read
	return p, nil
}

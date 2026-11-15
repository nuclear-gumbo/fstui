// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Device changes: online, offline, clear errors. Device names come from
// zpool status and go after "--".

// findDevice returns the device named dev and the vdev it is a member
// of (the root for a plain stripe).
func findDevice(st zfs.PoolStatus, dev string) (device, parent *zfs.Vdev) {
	var walk func(v, up *zfs.Vdev)
	walk = func(v, up *zfs.Vdev) {
		if device != nil {
			return
		}
		if v.Name.Raw() == dev && len(v.Children) == 0 {
			device, parent = v, up
			return
		}
		for i := range v.Children {
			walk(&v.Children[i], v)
		}
	}
	walk(&st.Data, nil)
	for _, g := range [][]zfs.Vdev{st.Special, st.Dedup, st.Logs, st.Cache, st.Spares} {
		for i := range g {
			walk(&g[i], &zfs.Vdev{Type: "root"})
		}
	}
	return device, parent
}

// spareCopies is how many members of parent may be missing while its
// data stays readable: one less than a mirror's width, a raidz's or
// draid's parity, none for a single device.
func spareCopies(parent *zfs.Vdev) int {
	switch {
	case parent == nil || parent.Type == "root":
		return 0
	case parent.Type == "mirror":
		return len(parent.Children) - 1
	case parent.Type == "raidz" || parent.Type == "draid":
		// zpool names them raidz1-0, raidz2-1, draid2:4d:6c:0s-0.
		n := strings.TrimPrefix(strings.TrimPrefix(parent.Name.Raw(), "raidz"), "draid")
		if p, err := strconv.Atoi(n[:1]); err == nil {
			return p
		}
	}
	return 0
}

// missing counts members of parent that are not ONLINE, or about to go.
func missing(parent *zfs.Vdev, going string) int {
	n := 0
	for _, c := range parent.Children {
		if c.State != "ONLINE" || c.Name.Raw() == going {
			n++
		}
	}
	return n
}

// DeviceChange plans online, offline or clear for one device of pool.
func (b Builder) DeviceChange(ctx context.Context, op mutate.Op, pool, dev string) (*mutate.Plan, error) {
	if _, err := name.ParsePool(pool); err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	st, err := b.R.PoolTopology(ctx, pool)
	if err != nil {
		return nil, err
	}
	device, parent := findDevice(st, dev)
	if device == nil {
		return nil, fmt.Errorf("%s has no device %s", show(pool), show(dev))
	}
	params := map[string]string{"pool": pool, "device": dev}
	argv := func(sub string) execute.Argv {
		return execute.Argv{Tool: execute.Zpool, Options: []string{sub}, Operands: []string{pool, dev}}
	}
	var p *mutate.Plan
	switch op { //nolint:exhaustive // device changes only
	case mutate.DeviceOnline:
		p = newPlan(op, "Bring "+show(dev)+" online", []string{dev}, params, argv("online"))
		affects(p, "the device is used again; a resilver brings it up to date with what was written while it was away")
		blockIf(p, device.State == "ONLINE", "it is already online")
	case mutate.DeviceOffline:
		p = newPlan(op, "Take "+show(dev)+" offline", []string{dev}, params, argv("offline"))
		b.offlinePlan(p, device, parent, dev)
	case mutate.DeviceClear:
		p = newPlan(op, "Clear the errors of "+show(dev), []string{dev}, params, argv("clear"))
		affects(p, fmt.Sprintf("its error counts (read %d, write %d, checksum %d) go back to zero", device.ReadErrors, device.WriteErrors, device.ChecksumErrors))
	default:
		return nil, fmt.Errorf("%s is not a device change", op)
	}
	blockIf(p, b.Unprivileged, "device changes need root")
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		st, err := b.R.PoolTopology(ctx, pool)
		if err != nil {
			return nil, err
		}
		f := mutate.Fingerprint{pool + " (pool guid)": strconv.FormatUint(st.GUID, 10)}
		if d, par := findDevice(st, dev); d != nil {
			f[dev+" (state)"] = d.State
			if par != nil {
				f[dev+" (other members not online)"] = strconv.Itoa(missing(par, dev))
			}
		}
		return f, nil
	}
	if p.Baseline, err = read(ctx); err != nil {
		return nil, err
	}
	p.Recheck = read
	return p, nil
}

// offlinePlan says what is left of the vdev's redundancy, and blocks
// what zpool refuses ("no valid replicas").
func (b Builder) offlinePlan(p *mutate.Plan, device, parent *zfs.Vdev, dev string) {
	blockIf(p, device.State == "OFFLINE", "it is already offline")
	spare := spareCopies(parent)
	after := missing(parent, dev)
	switch {
	case after > spare:
		p.Blocked = append(p.Blocked, "the pool's data on "+show(parent.Name.Raw())+" would no longer be readable without it (zpool: no valid replicas)")
	case after == spare:
		p.Flags.RedundancyLoss = true
		affects(p, show(parent.Name.Raw())+" keeps working with no redundancy left: one more failure there loses data")
	default:
		affects(p, fmt.Sprintf("%s keeps working; %d more %s can fail there without losing data", show(parent.Name.Raw()), spare-after,
			plural(spare-after, "device", "devices")))
	}
	affects(p, "it stays offline, also after a reboot, until it is brought online")
}

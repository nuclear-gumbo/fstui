// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/format"

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
		if p, err := strconv.Atoi(n[:min(len(n), 1)]); err == nil {
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
		affects(p, show(dev)+" is used again; a resilver brings it up to date with what was written while it was away")
		blockIf(p, device.State == "ONLINE", show(dev)+" is already online")
	case mutate.DeviceOffline:
		p = newPlan(op, "Take "+show(dev)+" offline", []string{dev}, params, argv("offline"))
		b.offlinePlan(p, device, parent, dev)
	case mutate.DeviceDetach:
		p = newPlan(op, "Detach "+show(dev), []string{dev}, params, argv("detach"))
		detachPlan(p, st, pool, device, parent, dev)
	case mutate.DeviceClear:
		p = newPlan(op, "Clear the errors of "+show(dev), []string{dev}, params, argv("clear"))
		affects(p, fmt.Sprintf("the error counts of %s (read %d, write %d, checksum %d) go back to zero", show(dev), device.ReadErrors, device.WriteErrors, device.ChecksumErrors))
	default:
		return nil, fmt.Errorf("%s is not a device change", op)
	}
	blockIf(p, b.Unprivileged, "device changes need root")
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		st, err := b.R.PoolTopology(ctx, pool)
		if err != nil {
			return nil, err
		}
		return poolDeviceFacts(st, pool, dev), nil
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
	blockIf(p, device.State == "OFFLINE", show(dev)+" is already offline")
	spare := spareCopies(parent)
	after := missing(parent, dev)
	switch {
	case after > spare:
		p.Blocked = append(p.Blocked, "the data on "+show(parent.Name.Raw())+" would no longer be readable without "+show(dev)+" (zpool: no valid replicas)")
	case after == spare:
		p.Flags.RedundancyLoss = true
		affects(p, show(parent.Name.Raw())+" keeps working with no redundancy left: one more failure there loses data")
	default:
		affects(p, fmt.Sprintf("%s keeps working; %d more %s can fail there without losing data", show(parent.Name.Raw()), spare-after,
			format.Plural(spare-after, "device", "devices")))
	}
	affects(p, show(dev)+" stays offline, also after a reboot, until it is brought online")
}

// poolDeviceFacts is what a device change depends on: the pool, the
// device's state, its vdev's members, and whether a checkpoint exists.
func poolDeviceFacts(st zfs.PoolStatus, pool, dev string) mutate.Fingerprint {
	f := mutate.Fingerprint{pool + " (pool guid)": strconv.FormatUint(st.GUID, 10), pool + " (checkpoint)": strconv.FormatBool(st.Checkpoint != nil)}
	if d, par := findDevice(st, dev); d != nil {
		f[dev+" (state)"] = d.State
		if par != nil {
			f[dev+" (vdev members)"] = strconv.Itoa(len(par.Children))
			f[dev+" (other members not online)"] = strconv.Itoa(missing(par, dev))
		}
	}
	return f
}

// detachPlan blocks detaching from anything but a mirror or a replacement
// in progress, and says what is left of the vdev's copies.
func detachPlan(p *mutate.Plan, st zfs.PoolStatus, pool string, device, parent *zfs.Vdev, dev string) {
	blockIf(p, st.Checkpoint != nil, show(pool)+" has a checkpoint: discard it first (zpool refuses to detach while one exists)")
	if parent == nil || (parent.Type != "mirror" && parent.Type != "replacing" && parent.Type != "spare") {
		blockIf(p, true, show(dev)+" is not a side of a mirror, and only those can be detached; to take a whole vdev out, remove it")
		return
	}
	healthy := 0
	for _, c := range parent.Children {
		if c.Name.Raw() != dev && c.State == "ONLINE" {
			healthy++
		}
	}
	vdev := show(parent.Name.Raw())
	// zpool lists the device being replaced (or stood in for) first and
	// the new device or the spare last.
	last := device.Name.Raw() == parent.Children[len(parent.Children)-1].Name.Raw()
	switch {
	case parent.Type == "replacing" && last:
		affects(p, "the replacement in "+vdev+" is cancelled: the device being replaced stays")
	case parent.Type == "replacing":
		blockIf(p, true, show(dev)+" is being replaced: it leaves the pool by itself when the resilver finishes, and before that it may hold the only complete copy")
	case parent.Type == "spare" && last:
		affects(p, "the hot spare goes back to the spares; the device it stood in for stays")
	case parent.Type == "spare":
		affects(p, "the hot spare takes its place for good")
	}
	switch {
	case healthy == 0:
		blockIf(p, true, "no other side of "+vdev+" is online: detaching it loses the data there")
	case healthy == 1 && parent.Type == "mirror":
		p.Flags.RedundancyLoss = true
		affects(p, vdev+" is left with one copy and no redundancy: one failure there loses data")
	case parent.Type == "mirror":
		affects(p, fmt.Sprintf("%s keeps %d copies", vdev, healthy))
	}
	p.Irreversible = []string{show(dev) + " leaves the pool: to use it again it must be attached anew and resilvered in full."}
}

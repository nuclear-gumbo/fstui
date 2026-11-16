// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"maps"
	"strconv"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// NewDeviceSpec is a device of a pool and the device to attach to it
// (making or widening a mirror) or to replace it with.
type NewDeviceSpec struct {
	Pool, Device string // Device as zpool status names it
	New          string // a path from the device inventory
	Overwrite    bool   // destroy a signature found on New
}

// DeviceNew plans zpool attach or zpool replace. Neither has a dry run:
// what zpool would refuse is checked here, and the device is checked as
// pool creation checks it.
func (b Builder) DeviceNew(ctx context.Context, op mutate.Op, s NewDeviceSpec) (*mutate.Plan, error) {
	if op != mutate.DeviceAttach && op != mutate.DeviceReplace {
		return nil, fmt.Errorf("%s is not attach or replace", op)
	}
	if b.Devices == nil {
		return nil, ErrNoInventory
	}
	pn, err := name.ParsePool(s.Pool)
	if err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(s.Pool), err)
	}
	st, err := b.R.PoolTopology(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	device, parent := findDevice(st, s.Device)
	if device == nil {
		return nil, fmt.Errorf("%s has no device %s", show(s.Pool), show(s.Device))
	}
	inv, err := b.Devices(ctx)
	if err != nil {
		return nil, err
	}
	params := map[string]string{"pool": s.Pool, "device": s.Device, "new": s.New, "overwrite": boolParam(s.Overwrite)}
	sub, summary := "attach", "Attach "+show(s.New)+" to "+show(s.Device)
	if op == mutate.DeviceReplace {
		sub, summary = "replace", "Replace "+show(s.Device)+" with "+show(s.New)
	}
	p := newPlan(op, summary, []string{s.Device}, params)
	blockIf(p, b.Unprivileged, "device changes need root")
	blockIf(p, s.New == "", "choose the new device")
	blockIf(p, st.Checkpoint != nil, show(s.Pool)+" has a checkpoint: discard it first (zpool refuses to "+sub+" while one exists)")
	target := s.Device
	switch {
	case op == mutate.DeviceAttach && parent != nil && parent.Type == "raidz":
		// Attaching to a raidz vdev widens it: zpool takes the vdev's name.
		target = parent.Name.Raw()
		p.Summary, p.Params["expand"] = "Expand "+show(target)+" with "+show(s.New), "true"
		if err := b.expandPlan(ctx, p, pn, st, parent, inv, s.New); err != nil {
			return nil, err
		}
	case op == mutate.DeviceAttach:
		attachPlan(p, st, parent, s.Device)
	default:
		replacePlan(p, st, device, parent, s.Device)
	}
	opts := checkNew(p, s, device, inv)
	p.Steps = []execute.Argv{{Tool: execute.Zpool, Options: append([]string{sub}, opts...), Operands: []string{s.Pool, target, s.New}}}
	b.watchNew(p, s, st, inv)
	return p, nil
}

// watchNew fingerprints the pool's device and the new device, and reads
// them again before running.
func (b Builder) watchNew(p *mutate.Plan, s NewDeviceSpec, st zfs.PoolStatus, inv []disks.Device) {
	read := func(st zfs.PoolStatus, inv []disks.Device) mutate.Fingerprint {
		f := poolDeviceFacts(st, s.Pool, s.Device)
		f[s.Pool+" (expanding)"] = strconv.FormatBool(st.Expanding())
		maps.Copy(f, deviceFacts([]string{s.New}, inv))
		return f
	}
	p.Baseline = read(st, inv)
	p.Recheck = func(ctx context.Context) (mutate.Fingerprint, error) {
		st, err := b.R.PoolTopology(ctx, s.Pool)
		if err != nil {
			return nil, err
		}
		inv, err := b.Devices(ctx)
		if err != nil {
			return nil, err
		}
		return read(st, inv), nil
	}
}

// checkNew checks the new device as pool creation does, and against the
// size of the device it mirrors or replaces; it returns -f when the
// device holds a signature that overwrite allows destroying.
func checkNew(p *mutate.Plan, s NewDeviceSpec, device *zfs.Vdev, inv []disks.Device) []string {
	if s.New == "" {
		return nil
	}
	var opts []string
	if judgeDevices(p, []string{s.New}, s.Overwrite, inv) {
		opts = []string{"-f"}
	}
	old, ok := lookup(inv, device.Path)
	if n, found := lookup(inv, s.New); ok && found && device.Path != "" && n.Size < old.Size {
		blockIf(p, true, fmt.Sprintf("%s (%s) is smaller than %s (%s)", show(s.New), iec(n.Size), show(s.Device), iec(old.Size)))
	}
	return opts
}

// attachPlan allows attaching to a mirror's member or to a device that
// is a top-level vdev of its own (data, special, dedup or log): either
// way the result is a mirror one device wider.
func attachPlan(p *mutate.Plan, st zfs.PoolStatus, parent *zfs.Vdev, dev string) {
	switch {
	case parent != nil && parent.Type == "mirror":
		affects(p, fmt.Sprintf("%s becomes a %d-way mirror; a resilver copies %s's data to the new device", show(parent.Name.Raw()), len(parent.Children)+1, show(dev)))
	case parent != nil && parent.Type == "root" && !isCacheOrSpare(st, dev):
		affects(p, show(dev)+" becomes a 2-way mirror with the new device; a resilver copies its data there")
	default:
		blockIf(p, true, show(dev)+" cannot have a device attached: only a mirror's member or a device that is a vdev of its own can (not a cache device or a spare)")
	}
}

// expandPlan checks a RAIDZ expansion (OpenZFS 2.3) and says what it
// gives and costs.
func (b Builder) expandPlan(ctx context.Context, p *mutate.Plan, pool name.Pool, st zfs.PoolStatus, vdev *zfs.Vdev, inv []disks.Device, newDev string) error {
	blockIf(p, st.Expanding(), show(st.ExpandingName())+" is being expanded: one expansion at a time")
	if err := b.requireFeature(ctx, p, pool, "raidz_expansion"); err != nil {
		return err
	}
	width, parity := len(vdev.Children), spareCopies(vdev)
	gain := "one device's worth of space"
	if n, ok := lookup(inv, newDev); ok {
		smallest := n.Size
		for _, c := range vdev.Children {
			if d, ok := lookup(inv, c.Path); ok && c.Path != "" && d.Size < smallest {
				smallest = d.Size
			}
		}
		gain = "about " + iec(smallest)
	}
	affects(p,
		fmt.Sprintf("%s grows from %d to %d devices: %s more, before parity padding", show(vdev.Name.Raw()), width, width+1, gain),
		fmt.Sprintf("data already written keeps its ratio (%d data to %d parity) until it is rewritten, so free space grows by less than a new %d-wide vdev would give", width-parity, parity, width+1),
		"every block of the vdev is copied across the devices in the background; the pool stays in use, and a scrub follows")
	p.Irreversible = []string{"A raidz vdev never gets narrower: the device stays in it until the pool is destroyed."}
	return nil
}

// replacePlan allows replacing a device of a data, special, dedup or log
// vdev; the old device leaves when the resilver ends.
func replacePlan(p *mutate.Plan, st zfs.PoolStatus, device, parent *zfs.Vdev, dev string) {
	switch {
	case isCacheOrSpare(st, dev):
		blockIf(p, true, show(dev)+" is a cache device or a spare, which are not replaced: remove it and add the new one")
	case parent != nil && (parent.Type == "replacing" || parent.Type == "spare"):
		blockIf(p, true, show(dev)+" is already being replaced, or a spare stands in for it: let the resilver finish, or detach first")
	default:
		affects(p, "a resilver copies the data "+show(dev)+" holds to the new device; when it ends, "+show(dev)+" leaves the pool")
		if device.State != "ONLINE" {
			affects(p, show(dev)+" is "+device.State+": the data is rebuilt from the other devices of its vdev")
		}
	}
}

func isCacheOrSpare(st zfs.PoolStatus, dev string) bool {
	for _, g := range [][]zfs.Vdev{st.Cache, st.Spares} {
		for _, v := range g {
			if v.Name.Raw() == dev {
				return true
			}
		}
	}
	return false
}

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

// topLevel finds the top-level vdev holding the leaf dev, and its class:
// data, special, dedup, log, cache or spare.
func topLevel(st zfs.PoolStatus, dev string) (*zfs.Vdev, string) {
	holds := func(v *zfs.Vdev) bool {
		d, _ := findDevice(zfs.PoolStatus{Data: zfs.Vdev{Type: "root", Children: []zfs.Vdev{*v}}}, dev)
		return d != nil
	}
	for i := range st.Data.Children {
		if holds(&st.Data.Children[i]) {
			return &st.Data.Children[i], "data"
		}
	}
	for _, g := range []struct {
		vdevs []zfs.Vdev
		class string
	}{{st.Special, "special"}, {st.Dedup, "dedup"}, {st.Logs, "log"}, {st.Cache, "cache"}, {st.Spares, "spare"}} {
		for i := range g.vdevs {
			if holds(&g.vdevs[i]) {
				return &g.vdevs[i], g.class
			}
		}
	}
	return nil, ""
}

// VdevRemove plans zpool remove of the top-level vdev that holds the
// device dev: the whole mirror for a mirror's member.
func (b Builder) VdevRemove(ctx context.Context, pool, dev string) (*mutate.Plan, error) {
	if _, err := name.ParsePool(pool); err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	st, err := b.R.PoolTopology(ctx, pool)
	if err != nil {
		return nil, err
	}
	top, class := topLevel(st, dev)
	if top == nil {
		return nil, fmt.Errorf("%s has no device %s", show(pool), show(dev))
	}
	target := top.Name.Raw()
	p := newPlan(mutate.VdevRemove, "Remove "+show(target)+" from "+show(pool), []string{target},
		map[string]string{"pool": pool, "device": dev, "class": class},
		execute.Argv{Tool: execute.Zpool, Options: []string{"remove"}, Operands: []string{pool, target}})
	blockIf(p, b.Unprivileged, "device changes need root")
	blockIf(p, st.Checkpoint != nil, show(pool)+" has a checkpoint: discard it first (zpool refuses to remove while one exists)")
	blockIf(p, st.Removing(), show(st.RemovingName())+" is being removed: one removal at a time")
	switch class {
	case "data", "special", "dedup":
		removeDataPlan(p, st, pool, top, class)
	case "log":
		affects(p, "synchronous writes go to the pool's data vdevs again: slower for databases, virtual machines and NFS")
	case "cache":
		affects(p, "reads it served come from the pool's disks again; what it held is dropped")
	case "spare":
		affects(p, show(pool)+" has one hot spare fewer")
	}
	if len(p.Blocked) == 0 {
		if out, err := b.R.RemoveDryRun(ctx, pool, target); err != nil {
			blockIf(p, true, firstLine(err))
		} else {
			p.Preview.Output = out
		}
	}
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		st, err := b.R.PoolTopology(ctx, pool)
		if err != nil {
			return nil, err
		}
		f := poolDeviceFacts(st, pool, dev)
		f[pool+" (removing)"] = strconv.FormatBool(st.Removing())
		f[pool+" (data vdevs)"] = strconv.Itoa(len(st.Data.Children))
		return f, nil
	}
	if p.Baseline, err = read(ctx); err != nil {
		return nil, err
	}
	p.Recheck = read
	return p, nil
}

// removeDataPlan checks what zpool remove needs for a vdev holding data:
// no raidz in the pool, another data vdev, and room there for its data.
func removeDataPlan(p *mutate.Plan, st zfs.PoolStatus, pool string, top *zfs.Vdev, class string) {
	raidz := top.Type == "raidz" || top.Type == "draid"
	for _, l := range DataLayouts(st) {
		raidz = raidz || strings.HasPrefix(l, "raidz") || l == "draid"
	}
	blockIf(p, raidz, "zpool remove cannot take "+show(top.Name.Raw())+" out of "+show(pool)+", which has raidz or draid vdevs")
	var free uint64
	others := 0
	for _, v := range st.Data.Children {
		if v.Name.Raw() != top.Name.Raw() {
			free += v.Size - min(v.Alloc, v.Size)
			others++
		}
	}
	blockIf(p, class == "data" && others == 0, show(top.Name.Raw())+" is the only data vdev of "+show(pool))
	blockIf(p, others > 0 && top.Alloc > free, fmt.Sprintf("%s holds %s and the other data vdevs have %s free", show(top.Name.Raw()), iec(top.Alloc), iec(free)))
	affects(p,
		fmt.Sprintf("the %s on %s are copied to the other vdevs in the background; %s stays in use, and the removal can be cancelled while it runs", iec(top.Alloc), show(top.Name.Raw()), show(pool)),
		"a table of where the copied blocks went stays in the pool for good, using some memory and one more lookup when they are read")
	if class == "special" || class == "dedup" {
		affects(p, "the metadata and small blocks on "+show(top.Name.Raw())+" move to the data vdevs, which are usually slower")
	}
	p.Irreversible = []string{"The table of moved blocks stays as long as the pool exists."}
}

// VdevRemoveCancel plans zpool remove -s: the removal stops, the data
// copied so far stays where it is, and the vdev stays in the pool.
func (b Builder) VdevRemoveCancel(ctx context.Context, pool string) (*mutate.Plan, error) {
	if _, err := name.ParsePool(pool); err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	st, err := b.R.PoolTopology(ctx, pool)
	if err != nil {
		return nil, err
	}
	p := newPlan(mutate.VdevRemoveCancel, "Cancel the removal in "+show(pool), []string{pool}, map[string]string{"pool": pool},
		execute.Argv{Tool: execute.Zpool, Options: []string{"remove", "-s"}, Operands: []string{pool}})
	blockIf(p, b.Unprivileged, "device changes need root")
	blockIf(p, !st.Removing(), show(pool)+" has no vdev being removed")
	if st.Removing() {
		affects(p, show(st.RemovingName())+" stays in the pool; what was copied so far stays where it went")
	}
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		st, err := b.R.PoolTopology(ctx, pool)
		if err != nil {
			return nil, err
		}
		return mutate.Fingerprint{pool + " (pool guid)": strconv.FormatUint(st.GUID, 10), pool + " (removing)": st.RemovingName() + " " + strconv.FormatBool(st.Removing())}, nil
	}
	if p.Baseline, err = read(ctx); err != nil {
		return nil, err
	}
	p.Recheck = read
	return p, nil
}

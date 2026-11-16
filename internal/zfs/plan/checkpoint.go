// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// What a checkpoint stops while it exists (zpool-checkpoint(8), tested on 2.3.9).
const checkpointBlocks = "while it exists, removing, attaching and detaching devices (RAIDZ expansion too), split and reguid are refused, " +
	"and deleting data frees no space: a busy pool can fill up"

// maxListed is how many created-since objects a rewind preview names.
const maxListed = 20

// Checkpoint plans taking, discarding, looking at or rewinding to
// pool's checkpoint.
func (b Builder) Checkpoint(ctx context.Context, op mutate.Op, pool string) (*mutate.Plan, error) {
	pn, err := name.ParsePool(pool)
	if err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	st, err := b.R.PoolTopology(ctx, pool)
	if err != nil {
		return nil, err
	}
	props, err := b.R.PoolProperties(ctx, pn)
	if err != nil {
		return nil, err
	}
	p, err := b.checkpointPlan(ctx, op, pn, st, props)
	if err != nil {
		return nil, err
	}
	blockIf(p, props.Properties["readonly"].Text.Raw() == "on",
		show(pool)+" is imported read-only (perhaps a look at its checkpoint), so nothing can change; to return to the present, export it (x) and import it again (I)")
	blockIf(p, b.Unprivileged, "checkpoints need root")
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		st, err := b.R.PoolTopology(ctx, pool)
		if err != nil {
			return nil, err
		}
		f := mutate.Fingerprint{pool + " (pool guid)": strconv.FormatUint(st.GUID, 10), pool + " (checkpoint)": mutate.Absent}
		if c := st.Checkpoint; c != nil {
			f[pool+" (checkpoint)"] = "taken " + c.Taken.UTC().Format(time.RFC3339)
		}
		return f, nil
	}
	if p.Baseline, err = read(ctx); err != nil {
		return nil, err
	}
	p.Recheck = read
	return p, nil
}

// checkpointPlan is the plan for one checkpoint operation, before the
// checks every one of them shares.
func (b Builder) checkpointPlan(ctx context.Context, op mutate.Op, pool name.Pool, st zfs.PoolStatus, props zfs.Pool) (*mutate.Plan, error) {
	params := map[string]string{"pool": pool.String()}
	argv := func(opts ...string) execute.Argv {
		return execute.Argv{Tool: execute.Zpool, Options: opts, Operands: []string{pool.String()}}
	}
	var p *mutate.Plan
	switch op { //nolint:exhaustive // checkpoint operations only
	case mutate.CheckpointTake:
		p = newPlan(op, "Take a checkpoint of "+show(pool.String()), []string{pool.String()}, params, argv("checkpoint"))
		blockIf(p, st.Checkpoint != nil, show(pool.String())+" has a checkpoint already (a pool has at most one)")
		featureBlock(p, props, "zpool_checkpoint")
		affects(p, show(pool.String())+" can be rewound to this moment, losing everything written after it", checkpointBlocks)
	case mutate.CheckpointDiscard:
		p = newPlan(op, "Discard the checkpoint of "+show(pool.String()), []string{pool.String()}, params, argv("checkpoint", "-d"))
		blockIf(p, st.Checkpoint == nil, show(pool.String())+" has no checkpoint")
		if c := st.Checkpoint; c != nil {
			affects(p, fmt.Sprintf("frees about %s held only by the checkpoint taken %s; device changes are possible again",
				iec(c.Space), c.Taken.Format(time.DateTime)))
		}
		p.Irreversible = []string{show(pool.String()) + " can no longer be rewound to the checkpoint."}
	case mutate.CheckpointLook:
		p = newPlan(op, "Look at "+show(pool.String())+" as it was at its checkpoint", []string{pool.String()}, params,
			execute.Argv{Tool: execute.Zpool, Options: []string{"export"}, Operands: []string{pool.String()}},
			execute.Argv{Tool: execute.Zpool, Options: append([]string{"import", "-o", "readonly=on", "--rewind-to-checkpoint"}, searchDirs(st)...), Operands: []string{pool.String()}})
		blockIf(p, st.Checkpoint == nil, show(pool.String())+" has no checkpoint")
		if err := b.lookPreview(ctx, p, pool, st); err != nil {
			return nil, err
		}
	case mutate.CheckpointRewind:
		p = newPlan(op, "Rewind "+show(pool.String())+" to its checkpoint", []string{pool.String()}, params,
			execute.Argv{Tool: execute.Zpool, Options: []string{"export"}, Operands: []string{pool.String()}},
			execute.Argv{Tool: execute.Zpool, Options: append([]string{"import", "--rewind-to-checkpoint"}, searchDirs(st)...), Operands: []string{pool.String()}})
		blockIf(p, st.Checkpoint == nil, show(pool.String())+" has no checkpoint")
		if err := b.rewindPreview(ctx, p, pool, st); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%s is not a checkpoint change", op)
	}
	return p, nil
}

// checkpointFeature blocks p unless the pool may take a checkpoint.
func (b Builder) checkpointFeature(ctx context.Context, p *mutate.Plan, pool name.Pool) error {
	return b.requireFeature(ctx, p, pool, "zpool_checkpoint")
}

// requireFeature reads the pool's properties and applies featureBlock.
func (b Builder) requireFeature(ctx context.Context, p *mutate.Plan, pool name.Pool, feature string) error {
	props, err := b.R.PoolProperties(ctx, pool)
	if err != nil {
		return err
	}
	featureBlock(p, props, feature)
	return nil
}

// featureBlock blocks p unless the pool's feature is enabled or active
// (in use): disabled, or left out by the pool's compatibility setting,
// zpool refuses what needs it.
func featureBlock(p *mutate.Plan, props zfs.Pool, feature string) {
	v := props.Properties["feature@"+feature].Text.Raw()
	blockIf(p, v != "enabled" && v != "active",
		"the "+feature+" feature of "+show(props.Name.String())+" is "+orDash(v)+"; enabling it (zpool upgrade, or its compatibility setting) comes first")
}

// rewindPreview lists what the rewind loses and blocks it for a pool
// that holds the running system's root, which cannot be exported.
func (b Builder) rewindPreview(ctx context.Context, p *mutate.Plan, pool name.Pool, st zfs.PoolStatus) error {
	objs, err := b.R.PoolObjects(ctx, pool)
	if err != nil {
		return err
	}
	blockRoot(p, objs)
	var since []string
	for _, o := range objs {
		// The root dataset is made with the pool, before any checkpoint.
		if c := st.Checkpoint; c != nil && o.Name != pool.String() && o.Properties["creation"].IsNumber && int64(o.Properties["creation"].Number) > c.Taken.Unix() { //nolint:gosec // a Unix time
			since = append(since, o.Name)
		}
	}
	slices.Sort(since)
	for i, n := range since {
		if i == maxListed {
			p.Preview.Destroys = append(p.Preview.Destroys, termtext.New(fmt.Sprintf("and %d more created since the checkpoint", len(since)-maxListed)))
			break
		}
		p.Preview.Destroys = append(p.Preview.Destroys, termtext.New(n))
	}
	if c := st.Checkpoint; c != nil {
		affects(p, show(pool.String())+" comes back as it was at "+c.Taken.Format(time.DateTime)+": every change since, on every dataset, is gone",
			"devices added since leave the pool, still labelled with its name: clear them (zpool labelclear) before using them again",
			show(pool.String())+" is exported first: every dataset is unmounted, and the export fails if one is busy, leaving the pool as it is",
			"if the import fails after the export, the pool stays exported; the import command below can be run again")
	}
	p.Irreversible = []string{"Everything written to the pool since the checkpoint is lost, and the checkpoint with it."}
	return nil
}

// lookPreview says what looking at the checkpoint does and how to come
// back; like a rewind, it needs the pool exported.
func (b Builder) lookPreview(ctx context.Context, p *mutate.Plan, pool name.Pool, st zfs.PoolStatus) error {
	objs, err := b.R.PoolObjects(ctx, pool)
	if err != nil {
		return err
	}
	blockRoot(p, objs)
	if c := st.Checkpoint; c != nil {
		affects(p, show(pool.String())+" is exported, then imported read-only as it was at "+c.Taken.Format(time.DateTime)+": its files can be looked at and copied, nothing is written, and the checkpoint and everything since stay",
			"to return to the present, export it (x) and import it again (I); to keep the past instead, rewind (w) from the present",
			"the export fails if a dataset is busy, leaving the pool as it is")
	}
	return nil
}

// blockRoot blocks p when one of the pool's filesystems is mounted at /:
// that pool cannot be exported from the running system.
func blockRoot(p *mutate.Plan, objs []zfs.Dataset) {
	for _, o := range objs {
		if o.Properties["mounted"].Text.Raw() == "yes" && o.Properties["mountpoint"].Text.Raw() == "/" {
			blockIf(p, true, show(o.Name)+" is the running system's root: the pool cannot be exported from this system")
		}
	}
}

// searchDirs are -d options naming the directories the pool's devices
// are in, so the import finds the same devices it was using.
func searchDirs(st zfs.PoolStatus) []string {
	var dirs []string
	st.Walk(func(v zfs.Vdev) {
		if d := filepath.Dir(v.Path); v.Path != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	})
	var out []string
	for _, d := range dirs {
		out = append(out, "-d", d)
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Hold plans "zfs hold [-r] tag snap...": a held snapshot cannot be
// destroyed until every hold on it is released.
func (b Builder) Hold(ctx context.Context, snaps []string, tag string, recursive bool) (*mutate.Plan, error) {
	if err := parseSnapshots(snaps); err != nil {
		return nil, err
	}
	if err := freeForm("the hold tag", tag); err != nil {
		return nil, err
	}
	opts := []string{"hold"}
	if recursive {
		opts = append(opts, "-r")
	}
	p := newPlan(mutate.SnapshotHold, holdSummary("Hold", snaps, tag), snaps,
		map[string]string{"snapshots": joinList(snaps), "tag": tag, "recursive": boolParam(recursive)},
		zfsArgv(opts, append([]string{tag}, snaps...)...))
	p.Flags.Recursive = recursive
	holds, err := b.R.Holds(ctx, snaps)
	if err != nil {
		return nil, err
	}
	for _, s := range snaps {
		affects(p, fmt.Sprintf("holds %s with tag %s", s, tag))
		blockIf(p, slices.Contains(holds[s], tag), show(s)+" already has a hold with tag "+show(tag))
	}
	if recursive {
		affects(p, "and the snapshots of the same name in every dataset below")
	}
	return p, b.watch(ctx, p, snaps)
}

// Release plans "zfs release [-r] tag snap...".
func (b Builder) Release(ctx context.Context, snaps []string, tag string, recursive bool) (*mutate.Plan, error) {
	if err := parseSnapshots(snaps); err != nil {
		return nil, err
	}
	if err := freeForm("the hold tag", tag); err != nil {
		return nil, err
	}
	opts := []string{"release"}
	if recursive {
		opts = append(opts, "-r")
	}
	p := newPlan(mutate.SnapshotRelease, holdSummary("Release hold", snaps, tag), snaps,
		map[string]string{"snapshots": joinList(snaps), "tag": tag, "recursive": boolParam(recursive)},
		zfsArgv(opts, append([]string{tag}, snaps...)...))
	p.Flags.Recursive = recursive
	holds, err := b.R.Holds(ctx, snaps)
	if err != nil {
		return nil, err
	}
	for _, s := range snaps {
		h := holds[s]
		if !slices.Contains(h, tag) {
			p.Blocked = append(p.Blocked, show(s)+" has no hold with tag "+show(tag))
			continue
		}
		rest := slices.DeleteFunc(slices.Clone(h), func(t string) bool { return t == tag })
		if len(rest) == 0 {
			affects(p, s+" can be destroyed afterwards (no holds left)")
		} else {
			affects(p, s+" stays held by: "+strings.Join(rest, ", "))
		}
	}
	return p, b.watch(ctx, p, snaps)
}

func holdSummary(verb string, snaps []string, tag string) string {
	what := show(snaps[0])
	if len(snaps) > 1 {
		what = fmt.Sprintf("%d snapshots", len(snaps))
	}
	return fmt.Sprintf("%s %s on %s", verb, show(tag), what)
}

// SnapshotRename plans "zfs rename snap ds@new" within the same dataset.
func (b Builder) SnapshotRename(ctx context.Context, snap, newShort string) (*mutate.Plan, error) {
	s, err := name.ParseSnapshot(snap)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", show(snap), err)
	}
	to, err := name.ParseSnapshot(s.Dataset().String() + "@" + newShort)
	if err != nil {
		return nil, fmt.Errorf("new name %s: %w", show(newShort), err)
	}
	p := newPlan(mutate.SnapshotRename, "Rename snapshot "+show(snap)+" to "+show(to.String()), []string{snap},
		map[string]string{"snapshot": snap, "new": newShort}, zfsArgv([]string{"rename"}, snap, to.String()))
	affects(p, "renames "+snap+" to "+to.String())
	holds, err := b.R.Holds(ctx, []string{snap})
	if err != nil {
		return nil, err
	}
	if h := holds[snap]; len(h) > 0 {
		affects(p, "its holds stay with it: "+strings.Join(h, ", "))
	}
	if err := b.watch(ctx, p, []string{snap, to.String()}); err != nil {
		return nil, err
	}
	blockIf(p, p.Baseline[to.String()] != mutate.Absent, show(to.String())+" already exists")
	return p, nil
}

// Bookmark plans "zfs bookmark snap ds#name".
func (b Builder) Bookmark(ctx context.Context, snap, bm string) (*mutate.Plan, error) {
	s, err := name.ParseSnapshot(snap)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", show(snap), err)
	}
	to, err := name.ParseBookmark(s.Dataset().String() + "#" + bm)
	if err != nil {
		return nil, fmt.Errorf("bookmark name %s: %w", show(bm), err)
	}
	p := newPlan(mutate.BookmarkCreate, "Create bookmark "+show(to.String()), []string{snap},
		map[string]string{"snapshot": snap, "bookmark": bm}, zfsArgv([]string{"bookmark"}, snap, to.String()))
	affects(p, "creates "+to.String()+", which keeps the point of "+snap+" for incremental sends but no data")
	if err := b.watch(ctx, p, []string{snap, to.String()}); err != nil {
		return nil, err
	}
	blockIf(p, p.Baseline[to.String()] != mutate.Absent, show(to.String())+" already exists")
	return p, nil
}

// Clone plans "zfs clone snap newds": a new filesystem or volume whose
// origin is snap, in the same pool.
func (b Builder) Clone(ctx context.Context, snap, newDS string) (*mutate.Plan, error) {
	s, err := name.ParseSnapshot(snap)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", show(snap), err)
	}
	d, err := name.ParseDataset(newDS)
	if err != nil {
		return nil, fmt.Errorf("new dataset %s: %w", show(newDS), err)
	}
	p := newPlan(mutate.SnapshotClone, "Clone "+show(snap)+" as "+show(newDS), []string{snap},
		map[string]string{"snapshot": snap, "dataset": newDS}, b.unprivilegedCreate(zfsArgv([]string{"clone"}, snap, newDS)))
	affects(p, "creates "+newDS+" with the contents of "+snap,
		snap+" cannot be destroyed while "+newDS+" exists, unless "+newDS+" is promoted")
	if b.Unprivileged {
		affects(p, unmountedNote)
	}
	parent, hasParent := parentOf(newDS)
	names := []string{snap, newDS}
	if hasParent {
		names = append(names, parent)
	}
	if err := b.watch(ctx, p, names); err != nil {
		return nil, err
	}
	blockIf(p, d.Pool() != s.Dataset().Pool(), "a clone must be in the same pool as its snapshot ("+show(s.Dataset().Pool().String())+")")
	blockIf(p, p.Baseline[newDS] != mutate.Absent, show(newDS)+" already exists")
	blockIf(p, !hasParent, "a pool's root dataset cannot be a clone")
	blockIf(p, hasParent && p.Baseline[parent] == mutate.Absent, show(parent)+" does not exist")
	if hasParent {
		return p, b.checkParent(ctx, p, parent, "create", "mount")
	}
	return p, nil
}

// parentOf returns the parent of a dataset name; false for a pool's
// root dataset.
func parentOf(ds string) (string, bool) {
	i := strings.LastIndex(ds, "/")
	if i < 0 {
		return ds, false
	}
	return ds[:i], true
}

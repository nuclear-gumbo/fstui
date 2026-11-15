// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// SnapshotDestroy plans "zfs destroy [-r] snap" for each snapshot. The
// preview comes from zfs destroy -nvp; since that does not check holds
// or clones, they are checked here and block the plan.
func (b Builder) SnapshotDestroy(ctx context.Context, snaps []string, recursive bool) (*mutate.Plan, error) {
	if err := parseSnapshots(snaps); err != nil {
		return nil, err
	}
	opts := []string{"destroy"}
	var dry []string
	if recursive {
		opts, dry = append(opts, "-r"), []string{"-r"}
	}
	summary := "Destroy snapshot " + show(snaps[0])
	confirm := ""
	if len(snaps) > 1 {
		summary = fmt.Sprintf("Destroy %d snapshots", len(snaps))
		confirm = fmt.Sprintf("destroy %d snapshots", len(snaps))
	}
	if recursive {
		summary += " and the snapshots of the same name below"
	}
	p := newPlan(mutate.SnapshotDestroy, summary, snaps, map[string]string{"snapshots": joinList(snaps), "recursive": boolParam(recursive)})
	p.TypeToConfirm = confirm
	p.Flags.Recursive = recursive
	p.Preview.Frees = 0
	var all []string
	var out strings.Builder
	for _, s := range snaps {
		p.Steps = append(p.Steps, zfsArgv(opts, s))
		pv, err := b.R.DestroyDryRun(ctx, dry, s)
		var refused *execute.CommandError
		if errors.As(err, &refused) {
			// The dry run itself refuses some destroys, such as a
			// snapshot with clones; zfs says why.
			p.Blocked = append(p.Blocked, "zfs says: "+refused.Stderr.FirstLine().Display())
			all = append(all, s)
			p.Preview.Frees = -1
			continue
		}
		if err != nil {
			return nil, err
		}
		all = append(all, pv.Destroys...)
		out.WriteString(pv.Output)
		if pv.Reclaim < 0 || p.Preview.Frees < 0 {
			p.Preview.Frees = -1
		} else {
			p.Preview.Frees += pv.Reclaim
		}
	}
	for _, n := range all {
		p.Preview.Destroys = append(p.Preview.Destroys, termtext.New(n))
	}
	p.Preview.Output = termtext.New(out.String())
	p.Irreversible = []string{"The snapshots listed are deleted, with any data that only they still hold."}
	if err := b.blockHeldOrCloned(ctx, p, all); err != nil {
		return nil, err
	}
	return p, b.watch(ctx, p, all)
}

// blockHeldOrCloned blocks the plan for snapshots with holds or clones,
// which zfs destroy would refuse.
func (b Builder) blockHeldOrCloned(ctx context.Context, p *mutate.Plan, snaps []string) error {
	facts, err := b.R.Get(ctx, snaps, []string{"userrefs", "clones"})
	if err != nil {
		return err
	}
	for _, s := range snaps {
		f := facts[s]
		if n, _ := strconv.Atoi(f["userrefs"]); n > 0 {
			p.Blocked = append(p.Blocked, fmt.Sprintf("%s has %d %s; release %s first", show(s), n,
				plural(n, "hold", "holds"), plural(n, "it", "them")))
		}
		if c := f["clones"]; c != "" && c != "-" {
			p.Blocked = append(p.Blocked, show(s)+" has clones ("+show(c)+"); destroy or promote them first")
		}
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Rollback plans "zfs rollback [-r] snap". Without destroyNewer it is
// blocked when anything is newer than snap; with it, the newer
// snapshots and bookmarks are listed as destroyed. Clones of newer
// snapshots block it either way: fstui does not offer -R.
func (b Builder) Rollback(ctx context.Context, snap string, destroyNewer bool) (*mutate.Plan, error) {
	s, err := name.ParseSnapshot(snap)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", show(snap), err)
	}
	ds := s.Dataset().String()
	points, err := b.R.PointsOf(ctx, ds)
	if err != nil {
		return nil, err
	}
	newer, err := newerThan(points, snap)
	if err != nil {
		return nil, err
	}
	opts := []string{"rollback"}
	if destroyNewer {
		opts = append(opts, "-r")
	}
	p := newPlan(mutate.Rollback, "Roll "+show(ds)+" back to "+show(snap), []string{snap},
		map[string]string{"snapshot": snap, "destroy_newer": boolParam(destroyNewer)}, zfsArgv(opts, snap))
	p.Flags.Recursive = destroyNewer
	p.Irreversible = []string{"Every change to " + show(ds) + " since " + show(snap) + " is discarded."}
	names := []string{ds, snap}
	for _, pt := range newer {
		names = append(names, pt.Name)
		p.Preview.Destroys = append(p.Preview.Destroys, termtext.New(pt.Name))
		for _, c := range pt.Clones {
			p.Blocked = append(p.Blocked, show(pt.Name)+" has the clone "+show(c)+"; fstui does not roll back past clones (zfs rollback -R)")
		}
	}
	if len(newer) > 0 && !destroyNewer {
		p.Blocked = append(p.Blocked, fmt.Sprintf("%d newer %s; choose to destroy them to roll back this far",
			len(newer), plural(len(newer), "snapshot or bookmark exists", "snapshots or bookmarks exist")))
		p.Preview.Destroys = nil
	}
	if len(p.Preview.Destroys) > 0 {
		p.Irreversible = append(p.Irreversible, "The newer snapshots and bookmarks listed are destroyed.")
	}
	return p, b.watchWith(ctx, p, names, b.newest(ds))
}

// newest fingerprints which snapshot or bookmark of ds is the newest, so
// one taken after the preview stops the rollback.
func (b Builder) newest(ds string) func(context.Context) (mutate.Fingerprint, error) {
	return func(ctx context.Context) (mutate.Fingerprint, error) {
		pts, err := b.R.PointsOf(ctx, ds)
		if err != nil {
			return nil, err
		}
		last := "none"
		if len(pts) > 0 {
			last = pts[len(pts)-1].Name
		}
		return mutate.Fingerprint{ds + " (newest snapshot or bookmark)": last}, nil
	}
}

func newerThan(points []zfs.Point, snap string) ([]zfs.Point, error) {
	for i, pt := range points {
		if pt.Name == snap {
			var out []zfs.Point
			for _, q := range points[i+1:] {
				if q.CreateTXG > pt.CreateTXG {
					out = append(out, q)
				}
			}
			return out, nil
		}
	}
	return nil, errors.New(show(snap) + " does not exist")
}

// Promote plans "zfs promote clone": the origin's snapshots up to the
// clone's origin move to the clone, and the dependency is reversed.
func (b Builder) Promote(ctx context.Context, clone string) (*mutate.Plan, error) {
	if _, err := name.ParseDataset(clone); err != nil {
		return nil, fmt.Errorf("dataset %s: %w", show(clone), err)
	}
	p := newPlan(mutate.Promote, "Promote "+show(clone), []string{clone},
		map[string]string{"dataset": clone}, execute.Argv{
			// zfs promote rejects "--"; clone is a validated dataset
			// name, which begins with a letter.
			Tool: execute.ZFS, Options: []string{"promote"}, Operands: []string{clone}, NoSeparator: true,
		})
	facts, err := b.R.Get(ctx, []string{clone}, []string{"origin"})
	if err != nil {
		return nil, err
	}
	origin := facts[clone]["origin"]
	if origin == "" || origin == "-" {
		p.Blocked = append(p.Blocked, show(clone)+" is not a clone")
		return p, b.watch(ctx, p, []string{clone})
	}
	moved, err := b.promoteMoves(ctx, p, clone, origin)
	if err != nil {
		return nil, err
	}
	return p, b.watch(ctx, p, append([]string{clone, origin}, moved...))
}

// promoteMoves lists the snapshots that move to the clone and blocks
// on a snapshot name the clone already has.
func (b Builder) promoteMoves(ctx context.Context, p *mutate.Plan, clone, origin string) ([]string, error) {
	o, err := name.ParseSnapshot(origin)
	if err != nil {
		return nil, fmt.Errorf("origin %s: %w", show(origin), err)
	}
	from, err := b.R.PointsOf(ctx, o.Dataset().String())
	if err != nil {
		return nil, err
	}
	own, err := b.R.PointsOf(ctx, clone)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, pt := range own {
		_, short, _ := strings.Cut(pt.Name, "@")
		have[short] = true
	}
	var moved []string
	for _, pt := range from {
		short, isSnap := strings.CutPrefix(pt.Name, o.Dataset().String()+"@")
		if !isSnap {
			continue // bookmarks stay
		}
		moved = append(moved, pt.Name)
		affects(p, "moves "+pt.Name+" to "+clone+"@"+short)
		blockIf(p, have[short], show(clone)+" already has a snapshot named "+show(short))
		if pt.Name == origin {
			break
		}
	}
	affects(p, o.Dataset().String()+" becomes a clone of "+clone+"@"+o.Short())
	return moved, nil
}

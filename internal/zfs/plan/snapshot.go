// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// SnapshotNameLayout is the default snapshot name, as a Go time layout.
const SnapshotNameLayout = "fstui-2006-01-02-150405"

// DefaultSnapshotName is the name offered for a new snapshot.
func DefaultSnapshotName(now time.Time) string { return now.Format(SnapshotNameLayout) }

// SnapshotCreate plans "zfs snapshot [-r] ds@snap ...": one command, so
// the snapshots of several datasets are taken atomically.
func (b Builder) SnapshotCreate(ctx context.Context, datasets []string, snap string, recursive bool) (*mutate.Plan, error) {
	if len(datasets) == 0 {
		return nil, errors.New("no dataset selected")
	}
	full := make([]string, len(datasets))
	for i, ds := range datasets {
		if _, err := name.ParseDataset(ds); err != nil {
			return nil, fmt.Errorf("dataset %s: %w", termtext.New(ds).Display(), err)
		}
		s, err := name.ParseSnapshot(ds + "@" + snap)
		if err != nil {
			return nil, fmt.Errorf("snapshot name %s: %w", termtext.New(snap).Display(), err)
		}
		full[i] = s.String()
	}
	created := full
	if recursive {
		var err error
		if created, err = b.recursiveNames(ctx, datasets, snap); err != nil {
			return nil, err
		}
	}
	opts := []string{"snapshot"}
	if recursive {
		opts = append(opts, "-r")
	}
	p := &mutate.Plan{
		ID: mutate.NewID(), Op: mutate.SnapshotCreate, Targets: datasets,
		Summary: snapshotSummary(datasets, snap, recursive, len(created)),
		Steps:   []execute.Argv{{Tool: execute.ZFS, Options: opts, Operands: full}},
		Flags:   mutate.Flags{Recursive: recursive},
		Params:  map[string]string{"datasets": joinList(datasets), "snapshot": snap, "recursive": boolParam(recursive)},
	}
	if len(created) > 1 { // one snapshot is already named in the summary
		for _, n := range created {
			p.Preview.Affects = append(p.Preview.Affects, termtext.New("creates "+n))
		}
	}
	// The datasets must be the ones selected, and none of the new names
	// may exist; descendants that appear later are snapshotted too, as
	// zfs snapshot -r does, and are not part of the check.
	if err := b.watch(ctx, p, append(append([]string{}, datasets...), created...)); err != nil {
		return nil, err
	}
	for _, n := range created {
		if p.Baseline[n] != mutate.Absent {
			p.Blocked = append(p.Blocked, termtext.New(n).Display()+" already exists")
		}
	}
	for _, ds := range datasets {
		if p.Baseline[ds] == mutate.Absent {
			p.Blocked = append(p.Blocked, termtext.New(ds).Display()+" does not exist")
		}
	}
	return p, nil
}

// recursiveNames lists every snapshot zfs snapshot -r would create.
func (b Builder) recursiveNames(ctx context.Context, datasets []string, snap string) ([]string, error) {
	var out []string
	for _, ds := range datasets {
		out = append(out, ds+"@"+snap)
		below, err := b.R.Descendants(ctx, ds)
		if err != nil {
			return nil, err
		}
		for _, d := range below {
			out = append(out, d+"@"+snap)
		}
	}
	return out, nil
}

func snapshotSummary(datasets []string, snap string, recursive bool, n int) string {
	s := termtext.New(snap).Display()
	if len(datasets) == 1 && !recursive {
		return "Create snapshot " + termtext.New(datasets[0]).Display() + "@" + s
	}
	what := termtext.New(datasets[0]).Display()
	if len(datasets) > 1 {
		what = fmt.Sprintf("%d datasets", len(datasets))
	}
	return fmt.Sprintf("Create snapshot @%s of %s and below (%d snapshots)", s, what, n)
}

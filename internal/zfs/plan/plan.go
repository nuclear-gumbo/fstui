// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

// Package plan builds mutate.Plan values for ZFS operations: the exact
// commands, the preview from a native dry run or a listing, the state
// the plan depends on and how to read it again before running.
//
// Every plan can be built again from its operation and Params, which is
// how a saved plan is applied: never by running stored commands.
package plan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Reader is what plans read: zfs.Reader implements it.
type Reader interface {
	Identify(ctx context.Context, names []string) (map[string]zfs.Identity, error)
	Descendants(ctx context.Context, ds string) ([]string, error)
	Get(ctx context.Context, names, props []string) (map[string]map[string]string, error)
	Holds(ctx context.Context, snaps []string) (map[string][]string, error)
	DestroyDryRun(ctx context.Context, opts []string, target string) (zfs.DestroyPreview, error)
	PointsOf(ctx context.Context, ds string) ([]zfs.Point, error)
	PoolActivity(ctx context.Context, pool string) (zfs.Activity, error)
	PoolTopology(ctx context.Context, pool string) (zfs.PoolStatus, error)
	ARCLimits() (zfs.ARCLimits, error)
	Modprobe() (content []byte, exists bool, err error)
}

// Builder builds plans against the system r reads.
type Builder struct {
	R Reader
	// Unprivileged is set when fstui does not run as root: on Linux only
	// root mounts, so new filesystems are created unmounted.
	Unprivileged bool
	// Permits reads the user's delegated permissions on a dataset; nil
	// as root. Changes that create something check its parent with it.
	Permits func(ctx context.Context, ds string) (map[string]bool, error)
}

// checkParent blocks p when the user lacks a delegated permission on
// the parent a change creates something in (zfs-allow(8): create and
// mount on the parent for create, clone and rename).
func (b Builder) checkParent(ctx context.Context, p *mutate.Plan, parent string, need ...string) error {
	if b.Permits == nil || p.Baseline[parent] == mutate.Absent {
		return nil
	}
	perms, err := b.Permits(ctx, parent)
	if err != nil {
		return err
	}
	var miss []string
	for _, n := range need {
		if !perms[n] {
			miss = append(miss, n)
		}
	}
	blockIf(p, len(miss) > 0, "needs root or delegated "+strings.Join(miss, ", ")+" on "+show(parent))
	return nil
}

// unmountedNote is what an unprivileged create or clone does.
const unmountedNote = "it is not mounted: on Linux only root mounts"

// unmountedMessage is what zfs 2.3.9 prints when it made a filesystem
// for a user who cannot mount it, exiting 1.
const unmountedMessage = "filesystem successfully created, but it may only be mounted by root"

// unprivilegedCreate marks a create or clone step as done despite that
// message, for a user other than root.
func (b Builder) unprivilegedCreate(a execute.Argv) execute.Argv {
	if b.Unprivileged {
		a.Done = unmountedMessage
	}
	return a
}

// fingerprint reads the identity of names; a missing name is "absent".
func (b Builder) fingerprint(ctx context.Context, names []string) (mutate.Fingerprint, error) {
	ids, err := b.R.Identify(ctx, names)
	if err != nil {
		return nil, err
	}
	f := make(mutate.Fingerprint, len(names))
	for _, n := range names {
		f[n] = mutate.Absent
		if id, ok := ids[n]; ok {
			f[n] = id.String()
		}
	}
	return f, nil
}

// watch sets the plan's baseline and re-check to the identities of names.
func (b Builder) watch(ctx context.Context, p *mutate.Plan, names []string) error {
	return b.watchWith(ctx, p, names, nil)
}

// watchWith is watch with further facts from extra, such as which
// snapshot is the newest of a dataset.
func (b Builder) watchWith(ctx context.Context, p *mutate.Plan, names []string, extra func(context.Context) (mutate.Fingerprint, error)) error {
	names = slices.Clone(names)
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		f, err := b.fingerprint(ctx, names)
		if err != nil || extra == nil {
			return f, err
		}
		more, err := extra(ctx)
		if err != nil {
			return nil, err
		}
		maps.Copy(f, more)
		return f, nil
	}
	f, err := read(ctx)
	if err != nil {
		return err
	}
	p.Baseline, p.Recheck = f, read
	return nil
}

// Build builds the plan for op from saved parameters.
func (b Builder) Build(ctx context.Context, op mutate.Op, params map[string]string) (*mutate.Plan, error) {
	p := params
	switch op { //nolint:exhaustive // operations are added with their builders
	case mutate.SnapshotCreate:
		return b.SnapshotCreate(ctx, splitList(p["datasets"]), p["snapshot"], p["recursive"] == "true")
	case mutate.SnapshotHold:
		return b.Hold(ctx, splitList(p["snapshots"]), p["tag"], p["recursive"] == "true")
	case mutate.SnapshotRelease:
		return b.Release(ctx, splitList(p["snapshots"]), p["tag"], p["recursive"] == "true")
	case mutate.SnapshotRename:
		return b.SnapshotRename(ctx, p["snapshot"], p["new"])
	case mutate.BookmarkCreate:
		return b.Bookmark(ctx, p["snapshot"], p["bookmark"])
	case mutate.SnapshotClone:
		return b.Clone(ctx, p["snapshot"], p["dataset"])
	case mutate.SnapshotDestroy:
		return b.SnapshotDestroy(ctx, splitList(p["snapshots"]), p["recursive"] == "true")
	case mutate.Rollback:
		return b.Rollback(ctx, p["snapshot"], p["destroy_newer"] == "true")
	case mutate.Promote:
		return b.Promote(ctx, p["dataset"])
	case mutate.DatasetCreate:
		if p["encrypted"] == "true" { // never saved; refuse rather than create it unencrypted
			return nil, mutate.ErrSecretNotSaved
		}
		return b.DatasetCreate(ctx, p["dataset"], prefixed(p, "prop."))
	case mutate.DatasetRename:
		return b.DatasetRename(ctx, p["dataset"], p["new"])
	case mutate.DatasetDestroy:
		return b.DatasetDestroy(ctx, p["dataset"], p["recursive"] == "true")
	case mutate.PropertySet:
		return b.PropertySet(ctx, p["dataset"], p["property"], p["value"])
	case mutate.PropertyInherit:
		return b.PropertyInherit(ctx, p["dataset"], p["property"])
	case mutate.Mount:
		return b.Mount(ctx, p["dataset"])
	case mutate.Unmount:
		return b.Unmount(ctx, p["dataset"], p["force"] == "true")
	case mutate.ScrubStart, mutate.ScrubPause, mutate.ScrubResume, mutate.ScrubStop, mutate.ErrorScrub,
		mutate.TrimStart, mutate.InitializeStart, mutate.PoolClear:
		return b.PoolChange(ctx, op, p["pool"])
	case mutate.DeviceOnline, mutate.DeviceOffline, mutate.DeviceClear:
		return b.DeviceChange(ctx, op, p["pool"], p["device"])
	case mutate.VolumeCreate:
		return b.VolumeCreate(ctx, p["dataset"], p["size"], p["sparse"] == "true", p["block"])
	case mutate.VolumeResize:
		return b.VolumeResize(ctx, p["dataset"], p["size"])
	case mutate.KeyUnload:
		return b.KeyUnload(ctx, p["dataset"])
	case mutate.ARCSet:
		return b.ARCSet(ctx, p["max"], p["min"])
	case mutate.ARCPersist:
		return b.ARCPersist(ctx, p["max"], p["min"])
	case mutate.KeyLoad, mutate.KeyChange:
		return nil, mutate.ErrSecretNotSaved
	}
	return nil, fmt.Errorf("fstui cannot build %s plans yet", op)
}

// prefixed returns the params whose key starts with prefix, without it.
func prefixed(params map[string]string, prefix string) map[string]string {
	out := map[string]string{}
	for k, v := range params {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			out[rest] = v
		}
	}
	return out
}

// listSep separates names in a parameter; no ZFS name contains it.
const listSep = "\n"

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, listSep)
}

func joinList(ss []string) string { return strings.Join(ss, listSep) }

func boolParam(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// newPlan starts a plan with a fresh ID.
func newPlan(op mutate.Op, summary string, targets []string, params map[string]string, steps ...execute.Argv) *mutate.Plan {
	return &mutate.Plan{ID: mutate.NewID(), Op: op, Summary: summary, Targets: targets, Params: params, Steps: steps}
}

func zfsArgv(opts []string, operands ...string) execute.Argv {
	return execute.Argv{Tool: execute.ZFS, Options: opts, Operands: operands}
}

// show makes a name safe to put in a summary or message.
func show(s string) string { return termtext.New(s).Display() }

func parseSnapshots(snaps []string) error {
	if len(snaps) == 0 {
		return errors.New("no snapshot selected")
	}
	for _, s := range snaps {
		if _, err := name.ParseSnapshot(s); err != nil {
			return fmt.Errorf("snapshot %s: %w", show(s), err)
		}
	}
	return nil
}

// maxFreeForm bounds free-form values such as hold tags.
const maxFreeForm = 255

// freeForm checks a value the user typed that ZFS takes as free text, such as a
// hold tag: not empty, no leading "-", no control or bidi characters.
func freeForm(what, v string) error {
	switch {
	case v == "":
		return fmt.Errorf("%s is empty", what)
	case strings.HasPrefix(v, "-"):
		return fmt.Errorf("%s must not begin with -", what)
	case len(v) > maxFreeForm:
		return fmt.Errorf("%s is longer than %d bytes", what, maxFreeForm)
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return fmt.Errorf("%s contains a control character", what)
		}
	}
	return nil
}

// blockIf adds a reason the plan cannot run when cond holds.
func blockIf(p *mutate.Plan, cond bool, reason string) {
	if cond {
		p.Blocked = append(p.Blocked, reason)
	}
}

func affects(p *mutate.Plan, lines ...string) {
	for _, l := range lines {
		p.Preview.Affects = append(p.Preview.Affects, termtext.New(l))
	}
}

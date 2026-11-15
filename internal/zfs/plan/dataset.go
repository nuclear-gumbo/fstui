// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

func parseDS(ds string) error {
	if _, err := name.ParseDataset(ds); err != nil {
		return fmt.Errorf("dataset %s: %w", show(ds), err)
	}
	return nil
}

// DatasetCreate plans "zfs create [-o prop=value]... ds" for a
// filesystem. Every property is given explicitly and checked first.
func (b Builder) DatasetCreate(ctx context.Context, ds string, props map[string]string) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	opts := []string{"create"}
	params := map[string]string{"dataset": ds}
	for _, k := range slices.Sorted(maps.Keys(props)) {
		if props[k] == "" {
			continue // inherit
		}
		v, err := checkProperty(k, props[k])
		if err != nil {
			return nil, err
		}
		opts = append(opts, "-o", k+"="+v)
		params["prop."+k] = props[k]
	}
	p := newPlan(mutate.DatasetCreate, "Create filesystem "+show(ds), []string{ds}, params, b.unprivilegedCreate(zfsArgv(opts, ds)))
	affects(p, "creates "+ds)
	if b.Unprivileged {
		affects(p, unmountedNote)
	}
	for _, o := range opts[1:] {
		if o != "-o" {
			affects(p, "with "+o)
		}
	}
	parent, hasParent := parentOf(ds)
	names := []string{ds}
	if hasParent {
		names = append(names, parent)
	}
	if err := b.watch(ctx, p, names); err != nil {
		return nil, err
	}
	blockIf(p, !hasParent, "a pool's root dataset is created with the pool")
	blockIf(p, p.Baseline[ds] != mutate.Absent, show(ds)+" already exists")
	blockIf(p, hasParent && p.Baseline[parent] == mutate.Absent, show(parent)+" does not exist")
	if hasParent {
		return p, b.checkParent(ctx, p, parent, "create", "mount")
	}
	return p, nil
}

// DatasetRename plans "zfs rename ds new" within the pool.
func (b Builder) DatasetRename(ctx context.Context, ds, to string) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	if err := parseDS(to); err != nil {
		return nil, err
	}
	p := newPlan(mutate.DatasetRename, "Rename "+show(ds)+" to "+show(to), []string{ds},
		map[string]string{"dataset": ds, "new": to}, zfsArgv([]string{"rename"}, ds, to))
	affects(p, "renames "+ds+" to "+to+", with everything below it and its snapshots")
	facts, err := b.R.Get(ctx, []string{ds}, []string{"mountpoint"})
	if err != nil {
		return nil, err
	}
	if mp := facts[ds]["mountpoint"]; mp != "" && mp != "-" && mp != "none" && mp != "legacy" {
		affects(p, "it is unmounted and mounted again; an inherited mountpoint moves with the name (now "+mp+")")
	}
	parent, _ := parentOf(to)
	if err := b.watch(ctx, p, []string{ds, to, parent}); err != nil {
		return nil, err
	}
	dp, _, _ := strings.Cut(ds, "/")
	tp, _, _ := strings.Cut(to, "/")
	blockIf(p, !strings.Contains(ds, "/"), "a pool's root dataset cannot be renamed")
	blockIf(p, dp != tp, "a dataset cannot be renamed into another pool")
	blockIf(p, strings.HasPrefix(to, ds+"/"), "a dataset cannot be moved below itself")
	blockIf(p, p.Baseline[to] != mutate.Absent, show(to)+" already exists")
	blockIf(p, p.Baseline[parent] == mutate.Absent, show(parent)+" does not exist")
	return p, b.checkParent(ctx, p, parent, "create", "mount")
}

// DatasetDestroy plans "zfs destroy [-r] ds" from zfs destroy -nvp.
// Without recursive, a dataset with children or snapshots is refused by
// the dry run, and that reason blocks the plan.
func (b Builder) DatasetDestroy(ctx context.Context, ds string, recursive bool) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	opts, dry := []string{"destroy"}, []string(nil)
	if recursive {
		opts, dry = append(opts, "-r"), []string{"-r"}
	}
	summary := "Destroy " + show(ds)
	if recursive {
		summary += " and everything below it"
	}
	p := newPlan(mutate.DatasetDestroy, summary, []string{ds},
		map[string]string{"dataset": ds, "recursive": boolParam(recursive)}, zfsArgv(opts, ds))
	p.Flags.Recursive = recursive
	p.Irreversible = []string{"Everything listed under Destroyed is deleted, files and snapshots, and cannot be brought back."}
	all := []string{ds}
	pv, err := b.R.DestroyDryRun(ctx, dry, ds)
	var refused *execute.CommandError
	switch {
	case errors.As(err, &refused):
		p.Blocked = append(p.Blocked, "zfs says: "+refused.Stderr.FirstLine().Display())
		p.Preview.Frees = -1
	case err != nil:
		return nil, err
	default:
		all = pv.Destroys
		p.Preview.Frees, p.Preview.Output = pv.Reclaim, termtext.New(pv.Output)
		for _, n := range pv.Destroys {
			p.Preview.Destroys = append(p.Preview.Destroys, termtext.New(n))
		}
	}
	blockIf(p, !strings.Contains(ds, "/"), "a pool's root dataset is destroyed with the pool")
	if err := b.blockHeldOrCloned(ctx, p, snapshotsIn(all)); err != nil {
		return nil, err
	}
	return p, b.watch(ctx, p, all)
}

func snapshotsIn(names []string) []string {
	var out []string
	for _, n := range names {
		if strings.Contains(n, "@") {
			out = append(out, n)
		}
	}
	return out
}

// PropertySet plans "zfs set prop=value ds".
func (b Builder) PropertySet(ctx context.Context, ds, prop, value string) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	v, err := checkProperty(prop, value)
	if err != nil {
		return nil, err
	}
	p := newPlan(mutate.PropertySet, "Set "+show(prop)+" of "+show(ds)+" to "+show(value), []string{ds},
		map[string]string{"dataset": ds, "property": prop, "value": value}, zfsArgv([]string{"set"}, prop+"="+v, ds))
	p.Property = prop
	return p, b.propertyPreview(ctx, p, ds, prop, "to "+value)
}

// PropertyInherit plans "zfs inherit prop ds".
func (b Builder) PropertyInherit(ctx context.Context, ds, prop string) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	if _, ok := settable[prop]; !ok && !userProp.MatchString(prop) {
		return nil, fmt.Errorf("fstui does not change %s", show(prop))
	}
	p := newPlan(mutate.PropertyInherit, "Inherit "+show(prop)+" for "+show(ds), []string{ds},
		map[string]string{"dataset": ds, "property": prop}, zfsArgv([]string{"inherit"}, prop, ds))
	p.Property = prop
	after := "to the inherited or default value"
	if parent, ok := parentOf(ds); ok {
		facts, err := b.R.Get(ctx, []string{parent}, []string{prop})
		if err != nil {
			return nil, err
		}
		if v, ok := facts[parent][prop]; ok {
			after = "to " + v + ", the value of " + parent
		}
	}
	return p, b.propertyPreview(ctx, p, ds, prop, after)
}

// propertyPreview shows the current value and what it becomes, says
// when only new data is affected, and fingerprints the current value so
// a change by someone else stops the plan.
func (b Builder) propertyPreview(ctx context.Context, p *mutate.Plan, ds, prop, becomes string) error {
	facts, err := b.R.Get(ctx, []string{ds}, []string{prop})
	if err != nil {
		return err
	}
	affects(p, fmt.Sprintf("%s of %s changes from %s %s", prop, ds, cmpOr(facts[ds][prop], "unset"), becomes))
	if h, ok := zfs.DatasetHelp(prop); ok && h.Effect == zfs.NewDataOnly {
		affects(p, h.Effect.Text())
	}
	key := ds + " " + prop
	return b.watchWith(ctx, p, []string{ds}, func(ctx context.Context) (mutate.Fingerprint, error) {
		f, err := b.R.Get(ctx, []string{ds}, []string{prop})
		if err != nil {
			return nil, err
		}
		return mutate.Fingerprint{key: f[ds][prop]}, nil
	})
}

func cmpOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

// Mount plans "zfs mount ds"; Unmount "zfs unmount [-f] ds".
func (b Builder) Mount(ctx context.Context, ds string) (*mutate.Plan, error) {
	return b.mountPlan(ctx, ds, true, false)
}

// Unmount plans "zfs unmount [-f] ds".
func (b Builder) Unmount(ctx context.Context, ds string, force bool) (*mutate.Plan, error) {
	return b.mountPlan(ctx, ds, false, force)
}

func (b Builder) mountPlan(ctx context.Context, ds string, mount, force bool) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	facts, err := b.R.Get(ctx, []string{ds}, []string{"mounted", "canmount", "mountpoint"})
	if err != nil {
		return nil, err
	}
	f := facts[ds]
	var p *mutate.Plan
	if mount {
		p = newPlan(mutate.Mount, "Mount "+show(ds), []string{ds}, map[string]string{"dataset": ds}, zfsArgv([]string{"mount"}, ds))
		affects(p, "mounts "+ds+" at "+f["mountpoint"])
		blockIf(p, f["mounted"] == "yes", show(ds)+" is already mounted")
		blockIf(p, f["canmount"] == "off", show(ds)+" has canmount=off")
		blockIf(p, f["mountpoint"] == "none" || f["mountpoint"] == "legacy", show(ds)+" has mountpoint="+f["mountpoint"]+"; zfs mount does not mount it")
	} else {
		opts := []string{"unmount"}
		if force {
			opts = append(opts, "-f")
		}
		p = newPlan(mutate.Unmount, "Unmount "+show(ds), []string{ds},
			map[string]string{"dataset": ds, "force": boolParam(force)}, zfsArgv(opts, ds))
		p.Flags.Force = force
		affects(p, "unmounts "+ds+" from "+f["mountpoint"]+"; programs using files there lose them")
		if force {
			p.Irreversible = []string{"Forcing unmounts even with files open; programs using them may lose unsaved data."}
		}
		blockIf(p, f["mounted"] != "yes", show(ds)+" is not mounted")
	}
	key := ds + " mounted"
	return p, b.watchWith(ctx, p, []string{ds}, func(ctx context.Context) (mutate.Fingerprint, error) {
		g, err := b.R.Get(ctx, []string{ds}, []string{"mounted"})
		if err != nil {
			return nil, err
		}
		return mutate.Fingerprint{key: g[ds]["mounted"]}, nil
	})
}

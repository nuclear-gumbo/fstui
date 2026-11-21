// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/tune"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// ZFSOffers returns the changes offered for ZFS selections. On a pool
// imported read-only every change but export is unavailable, with the
// way back (readOnly).
func ZFSOffers(b plan.Builder, now func() time.Time) func(ui.Target) (string, []Offer) {
	offers := zfsOffers(b, now)
	return func(t ui.Target) (string, []Offer) {
		title, list := offers(t)
		return title, readOnly(b, poolOf(t), list)
	}
}

// poolOf is the pool a selection is in, or "".
func poolOf(t ui.Target) string {
	switch {
	case len(t.Names) == 0 || t.Kind == "arc" || t.Kind == "pools":
		return ""
	case t.Kind == "device":
		return t.Parent
	case t.Kind == "tune":
		return t.Names[0]
	}
	pool, _, _ := strings.Cut(t.Names[0], "/")
	pool, _, _ = strings.Cut(pool, "@")
	return pool
}

// readOnly marks every change on a pool imported read-only (a look at
// its checkpoint, or an import with readonly=on) as unavailable but
// export, import and creating another pool, saying how to return to
// the present. The pool's state is read once, when the menu opens.
func readOnly(b plan.Builder, pool string, offers []Offer) []Offer {
	if pool == "" || b.R == nil {
		return offers
	}
	var once sync.Once
	var ro bool
	isRO := func(ctx context.Context) bool {
		once.Do(func() {
			pn, err := name.ParsePool(pool)
			if err != nil {
				return
			}
			props, err := b.R.PoolProperties(ctx, pn)
			ro = err == nil && props.Properties["readonly"].Text.Raw() == "on"
		})
		return ro
	}
	why := pool + " is imported read-only (perhaps a look at its checkpoint), so nothing in it can change; to return to the present, export it (x) and import it again (I)"
	for i := range offers {
		switch offers[i].Op { //nolint:exhaustive // only these leave a read-only pool alone
		case mutate.PoolExport, mutate.PoolImport, mutate.PoolCreate:
			continue
		}
		check := offers[i].Check
		offers[i].Check = func(ctx context.Context) string {
			if isRO(ctx) {
				return termtext.New(why).Display()
			}
			if check != nil {
				return check(ctx)
			}
			return ""
		}
	}
	return offers
}

// zfsOffers are the changes for a selection, before the read-only check.
func zfsOffers(b plan.Builder, now func() time.Time) func(ui.Target) (string, []Offer) {
	return func(t ui.Target) (string, []Offer) {
		if t.Backend != "zfs" || len(t.Names) == 0 {
			return "", nil
		}
		n := t.Names[0]
		if t.Kind == "tune" { // the pool, then the changes, not marked items
			return "what the findings in " + n + " point to", tuneOffers(b, t.Names[1:])
		}
		if len(t.Names) > 1 {
			return batchOffers(b, t, now)
		}
		switch t.Kind {
		case "filesystem":
			offers := append([]Offer{snapshotCreate(b, t.Names, now), createChild(b, n), createEncrypted(b, n), createVolume(b, n)},
				datasetOffers(b, n, true)...)
			return n, append(offers, keyOffers(b, n)...)
		case "volume":
			return n, append([]Offer{snapshotCreate(b, t.Names, now), resizeVolume(b, n)}, datasetOffers(b, n, false)...)
		case "snapshot":
			return n, snapshotOffers(b, n)
		case "pool":
			return n, append(poolOffers(b, n), createPool(b), importPool(b))
		case "pools": // no pool imported: a new one, or an import
			return "pools", []Offer{createPool(b), importPool(b)}
		case "arc":
			return "ARC limits", arcOffers(b)
		case "device":
			return n + " in " + t.Parent, deviceOffers(b, t.Parent, n)
		}
		return n, []Offer{}
	}
}

func snapshotCreate(b plan.Builder, datasets []string, now func() time.Time) Offer {
	return Offer{
		Key: "s", Op: mutate.SnapshotCreate, Need: needs("snapshot", "mount"), Name: "Create snapshot",
		Fields: []Field{
			{Name: "snapshot", Label: "Snapshot name", Default: plan.DefaultSnapshotName(now())},
			{Name: "recursive", Label: "Also every dataset below (-r)", Default: "false", Switch: true},
		},
		Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.SnapshotCreate(ctx, datasets, v["snapshot"], v["recursive"] == "true")
		},
	}
}

func promote(b plan.Builder, ds string) Offer {
	build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) { return b.Promote(ctx, ds) }
	return Offer{
		Key: "p", Op: mutate.Promote, Need: needs("promote", "mount"), Name: "Promote clone", Build: build,
		Check: func(ctx context.Context) string { // most datasets are not clones
			p, err := build(ctx, nil)
			if err != nil {
				return err.Error()
			}
			return strings.Join(p.Blocked, "; ")
		},
	}
}

// snapshotOffers are the changes to one snapshot.
func snapshotOffers(b plan.Builder, snap string) []Offer {
	ds, short, _ := strings.Cut(snap, "@")
	recursive := Field{Name: "recursive", Label: "Also the snapshots of the same name below (-r)", Default: "false", Switch: true}
	tag := Field{Name: "tag", Label: "Hold tag", Default: "keep"}
	one := []string{snap}
	return []Offer{
		{Key: "h", Op: mutate.SnapshotHold, Need: needs("hold"), Name: "Hold", Fields: []Field{tag, recursive}, Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.Hold(ctx, one, v["tag"], v["recursive"] == "true")
		}},
		{Key: "u", Op: mutate.SnapshotRelease, Need: needs("release"), Name: "Release a hold", Fields: []Field{tag, recursive}, Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.Release(ctx, one, v["tag"], v["recursive"] == "true")
		}},
		{
			Key: "b", Op: mutate.BookmarkCreate, Need: needs("bookmark"), Name: "Create bookmark", Fields: []Field{{Name: "bookmark", Label: "Bookmark name", Default: short}},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.Bookmark(ctx, snap, v["bookmark"])
			},
		},
		{
			Key: "c", Op: mutate.SnapshotClone, Need: needs("clone", "create", "mount"), Name: "Clone", Fields: []Field{{Name: "dataset", Label: "New dataset", Default: ds + "-" + short}},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.Clone(ctx, snap, v["dataset"])
			},
		},
		{
			Key: "r", Op: mutate.SnapshotRename, Need: needs("rename"), Name: "Rename", Fields: []Field{{Name: "new", Label: "New snapshot name", Default: short}},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.SnapshotRename(ctx, snap, v["new"])
			},
		},
		{
			Key: "R", Op: mutate.Rollback, Need: needs("rollback", "mount"), Name: "Roll back to this snapshot", Fields: []Field{{Name: "destroy_newer", Label: "Destroy newer snapshots and bookmarks (-r)", Default: "false", Switch: true}},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.Rollback(ctx, snap, v["destroy_newer"] == "true")
			},
		},
		{Key: "d", Op: mutate.SnapshotDestroy, Need: needs("destroy", "mount"), Name: "Destroy", Fields: []Field{recursive}, Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.SnapshotDestroy(ctx, one, v["recursive"] == "true")
		}},
	}
}

func createChild(b plan.Builder, parent string) Offer {
	return Offer{
		Key: "n", Op: mutate.DatasetCreate, Need: needs("create", "mount"), Name: "Create filesystem below",
		Fields: []Field{
			{Name: "dataset", Label: "Name", Default: parent + "/"},
			{Name: "mountpoint", Label: "Mountpoint (leave empty to inherit)", Optional: true},
			{Name: "compression", Label: "Compression (leave empty to inherit)", Optional: true},
			{Name: "quota", Label: "Quota (leave empty for none)", Optional: true},
		},
		Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.DatasetCreate(ctx, v["dataset"], map[string]string{
				"mountpoint": v["mountpoint"], "compression": v["compression"], "quota": v["quota"],
			})
		},
	}
}

// datasetOffers are the changes to a filesystem or volume; mounting
// applies to filesystems only.
func datasetOffers(b plan.Builder, ds string, fs bool) []Offer {
	out := []Offer{
		promote(b, ds),
		{
			Key: "m", Op: mutate.DatasetRename, Need: needs("rename", "create", "mount"), Name: "Rename", Fields: []Field{{Name: "new", Label: "New name", Default: ds}},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.DatasetRename(ctx, ds, v["new"])
			},
		},
		{
			Key: "e", Op: mutate.PropertySet, Need: propertyNeed, Name: "Set a property", Fields: []Field{{Name: "property", Label: "Property"}, {Name: "value", Label: "Value"}},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.PropertySet(ctx, ds, v["property"], v["value"])
			},
		},
		{
			Key: "i", Op: mutate.PropertyInherit, Need: propertyNeed, Name: "Inherit a property", Fields: []Field{{Name: "property", Label: "Property"}},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.PropertyInherit(ctx, ds, v["property"])
			},
		},
	}
	if fs {
		mountOnly := ""
		if b.Unprivileged {
			mountOnly = "on Linux only root mounts and unmounts"
		}
		out = append(
			out,
			Offer{Key: "o", Op: mutate.Mount, Need: needs("mount"), Name: "Mount", Disabled: mountOnly, Build: func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) { return b.Mount(ctx, ds) }},
			Offer{
				Key: "U", Op: mutate.Unmount, Need: needs("mount"), Name: "Unmount", Disabled: mountOnly, Fields: []Field{{Name: "force", Label: "Even with files open (-f)", Default: "false", Switch: true}},
				Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
					return b.Unmount(ctx, ds, v["force"] == "true")
				},
			},
		)
	}
	return append(out, Offer{
		Key: "D", Op: mutate.DatasetDestroy, Need: needs("destroy", "mount"), Name: "Destroy", Fields: []Field{{Name: "recursive", Label: "With everything below it (-r)", Default: "false", Switch: true}},
		Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.DatasetDestroy(ctx, ds, v["recursive"] == "true")
		},
	})
}

// propertyNeed: setting or inheriting a property needs that property's
// permission, or userprop for user properties (zfs-allow(8)). With no
// values yet it needs nothing, so the menu leaves the check to the form.
func propertyNeed(v map[string]string) []string {
	p := v["property"]
	switch {
	case v == nil:
		return nil
	case strings.Contains(p, ":"):
		return []string{"userprop"}
	}
	return []string{p}
}

// createPool opens the form for a new pool. It needs root and lsblk.
func createPool(b plan.Builder) Offer {
	return withPoolForm(b, Offer{Key: "N", Op: mutate.PoolCreate, Name: "Create a new pool"}, "")
}

// importPool opens the list of pools zpool import finds. It needs root.
func importPool(b plan.Builder) Offer {
	o := Offer{Key: "I", Op: mutate.PoolImport, Name: "Import a pool", Open: func(confirm func(*mutate.Plan, Rebuild) ui.Modal) (ui.Modal, tea.Cmd) {
		return openImportForm(b, confirm)
	}}
	if b.Unprivileged {
		o.Disabled = "needs root"
	}
	return o
}

// withPoolForm makes o open the pool form, for a new pool or for devices to
// add to pool, and disables it without root or lsblk.
func withPoolForm(b plan.Builder, o Offer, pool string) Offer {
	o.Open = func(confirm func(*mutate.Plan, Rebuild) ui.Modal) (ui.Modal, tea.Cmd) {
		return openPoolForm(b, pool, confirm)
	}
	o.Disabled = inventoryMissing(b)
	return o
}

// inventoryMissing is why a change that lists the disks cannot be made:
// it needs root, and lsblk to list them.
func inventoryMissing(b plan.Builder) string {
	switch {
	case b.Unprivileged:
		return "needs root"
	case b.Devices == nil:
		return "needs lsblk to list the disks, and fstui did not find it"
	}
	return ""
}

// poolOffers are the changes to a pool. Pool changes need root; the
// planner says so for other users.
func poolOffers(b plan.Builder, pool string) []Offer {
	// One read of the pool's activity answers every offer's check.
	avail := sync.OnceValues(func() (map[mutate.Op]string, error) {
		return b.PoolAvailability(context.Background(), pool)
	})
	offer := func(key, name string, op mutate.Op) Offer {
		return Offer{
			Key: key, Name: name, Op: op,
			Build: func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
				return b.PoolChange(ctx, op, pool)
			},
			Check: func(context.Context) string {
				reasons, err := avail()
				if err != nil {
					return "cannot read the state of " + pool + ": " + err.Error()
				}
				return reasons[op]
			},
		}
	}
	return []Offer{
		offer("s", "Start a scrub", mutate.ScrubStart),
		offer("p", "Pause the scrub", mutate.ScrubPause),
		offer("r", "Resume the scrub", mutate.ScrubResume),
		offer("S", "Stop the scrub", mutate.ScrubStop),
		offer("e", "Check known errors only", mutate.ErrorScrub),
		offer("t", "Trim", mutate.TrimStart),
		offer("i", "Initialize free space", mutate.InitializeStart),
		offer("c", "Clear errors", mutate.PoolClear),
		withPoolForm(b, Offer{Key: "A", Op: mutate.PoolAdd, Name: "Add devices"}, pool),
		checkpointOffer(b, "C", "Take a checkpoint", mutate.CheckpointTake, pool),
		checkpointOffer(b, "D", "Discard the checkpoint", mutate.CheckpointDiscard, pool),
		checkpointOffer(b, "L", "Look at the checkpoint, read-only", mutate.CheckpointLook, pool),
		checkpointOffer(b, "w", "Rewind to the checkpoint", mutate.CheckpointRewind, pool),
		cancelRemoval(b, pool),
		exportOffer(b, pool),
		upgradeOffer(b, pool),
	}
}

// removeOffer removes the device's top-level vdev: a mirror's member
// takes the whole mirror with it, which the plan's summary names.
func removeOffer(b plan.Builder, pool, dev string) Offer {
	build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
		return b.VdevRemove(ctx, pool, dev)
	}
	return Offer{Key: "R", Name: "Remove from the pool (with its mirror, if any)", Op: mutate.VdevRemove, Build: build, Check: blockedReasons(build)}
}

func upgradeOffer(b plan.Builder, pool string) Offer {
	build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) { return b.PoolUpgrade(ctx, pool) }
	return Offer{Key: "u", Name: "Upgrade: enable new features", Op: mutate.PoolUpgrade, Build: build, Check: blockedReasons(build)}
}

func exportOffer(b plan.Builder, pool string) Offer {
	build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) { return b.PoolExport(ctx, pool) }
	return Offer{Key: "x", Name: "Export the pool", Op: mutate.PoolExport, Build: build, Check: blockedReasons(build)}
}

func cancelRemoval(b plan.Builder, pool string) Offer {
	build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
		return b.VdevRemoveCancel(ctx, pool)
	}
	return Offer{Key: "X", Name: "Cancel the removal of a vdev", Op: mutate.VdevRemoveCancel, Build: build, Check: blockedReasons(build)}
}

// checkpointOffer is one checkpoint change; its check is the planner's
// own reasons, so the menu says why it cannot be done.
func checkpointOffer(b plan.Builder, key, name string, op mutate.Op, pool string) Offer {
	build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
		return b.Checkpoint(ctx, op, pool)
	}
	return Offer{Key: key, Name: name, Op: op, Build: build, Check: blockedReasons(build)}
}

// blockedReasons checks an offer by planning it: the plan's blocks, or
// the error, are why it cannot be done now.
func blockedReasons(build func(context.Context, map[string]string) (*mutate.Plan, error)) func(context.Context) string {
	return func(ctx context.Context) string {
		p, err := build(ctx, nil)
		if err != nil {
			return err.Error()
		}
		return strings.Join(p.Blocked, "; ")
	}
}

// deviceOffers are the changes to one device of a pool.
func deviceOffers(b plan.Builder, pool, dev string) []Offer {
	offer := func(key, name string, op mutate.Op) Offer {
		build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
			return b.DeviceChange(ctx, op, pool, dev)
		}
		return Offer{Key: key, Name: name, Op: op, Build: build, Check: blockedReasons(build)}
	}
	pick := func(key, name string, op mutate.Op) Offer {
		return Offer{Key: key, Name: name, Op: op, Disabled: inventoryMissing(b), Open: func(confirm func(*mutate.Plan, Rebuild) ui.Modal) (ui.Modal, tea.Cmd) {
			return openPickForm(b, op, pool, dev, confirm)
		}}
	}
	return []Offer{
		offer("o", "Bring online", mutate.DeviceOnline),
		offer("f", "Take offline", mutate.DeviceOffline),
		offer("c", "Clear its errors", mutate.DeviceClear),
		pick("a", "Attach a device: mirror it, or widen its raidz", mutate.DeviceAttach),
		pick("r", "Replace it", mutate.DeviceReplace),
		offer("d", "Detach it from its mirror", mutate.DeviceDetach),
		removeOffer(b, pool, dev),
	}
}

func createVolume(b plan.Builder, parent string) Offer {
	return Offer{
		Key: "v", Op: mutate.VolumeCreate, Need: needs("create", "mount"), Name: "Create volume below",
		Fields: []Field{
			{Name: "dataset", Label: "Name", Default: parent + "/"},
			{Name: "size", Label: "Size (e.g. 20G)"},
			{Name: "sparse", Label: "Sparse: reserve no space (-s)", Default: "false", Switch: true},
			{Name: "block", Label: "Block size (leave empty for the default)", Optional: true},
		},
		Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.VolumeCreate(ctx, v["dataset"], v["size"], v["sparse"] == "true", v["block"])
		},
	}
}

func resizeVolume(b plan.Builder, ds string) Offer {
	return Offer{
		Key: "z", Op: mutate.VolumeResize, Need: needs("volsize"), Name: "Resize",
		Fields: []Field{{Name: "size", Label: "New size (e.g. 40G)"}},
		Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
			return b.VolumeResize(ctx, ds, v["size"])
		},
	}
}

// passphraseFields ask for a new passphrase twice.
var passphraseFields = []Field{
	{Name: "passphrase", Label: "Passphrase (at least 8 characters)", Secret: true},
	{Name: "repeat", Label: "Retype passphrase", Secret: true, Repeat: "passphrase"},
}

func createEncrypted(b plan.Builder, parent string) Offer {
	return Offer{
		Key: "E", Op: mutate.DatasetCreate, Need: needs("create", "mount", "encryption", "keyformat", "keylocation"), Name: "Create encrypted filesystem below",
		Fields: append([]Field{{Name: "dataset", Label: "Name", Default: parent + "/"}}, passphraseFields...),
		BuildSecret: func(ctx context.Context, v map[string]string, secret []byte) (*mutate.Plan, error) {
			return b.EncryptedCreate(ctx, v["dataset"], nil, secret)
		},
	}
}

// keyOffers load, unload and change the key of an encryption root.
func keyOffers(b plan.Builder, ds string) []Offer {
	avail := sync.OnceValues(func() (map[mutate.Op]string, error) { return b.KeyAvailability(context.Background(), ds) })
	check := func(op mutate.Op) func(context.Context) string {
		return func(context.Context) string {
			reasons, err := avail()
			if err != nil {
				return "cannot read the encryption state of " + ds + ": " + err.Error()
			}
			return reasons[op]
		}
	}
	return []Offer{
		{
			Key: "l", Op: mutate.KeyLoad, Need: needs("load-key", "mount"), Name: "Load key", Check: check(mutate.KeyLoad),
			Fields: []Field{{Name: "passphrase", Label: "Passphrase", Secret: true}},
			BuildSecret: func(ctx context.Context, _ map[string]string, secret []byte) (*mutate.Plan, error) {
				return b.KeyLoad(ctx, ds, secret)
			},
		},
		{
			Key: "u", Op: mutate.KeyUnload, Need: needs("load-key", "mount"), Name: "Unload key", Check: check(mutate.KeyUnload),
			Build: func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) { return b.KeyUnload(ctx, ds) },
		},
		{
			Key: "c", Op: mutate.KeyChange, Need: needs("change-key"), Name: "Change passphrase", Check: check(mutate.KeyChange),
			Fields: passphraseFields,
			BuildSecret: func(ctx context.Context, _ map[string]string, secret []byte) (*mutate.Plan, error) {
				return b.KeyChange(ctx, ds, secret)
			},
		},
	}
}

// arcOffers set the ARC's limits now, or for the next boot.
func arcOffers(b plan.Builder) []Offer {
	fields := []Field{
		{Name: "max", Label: "Maximum (e.g. 4G; leave empty to keep it)", Optional: true},
		{Name: "min", Label: "Minimum (leave empty to keep it)", Optional: true},
	}
	return []Offer{
		{
			Key: "s", Op: mutate.ARCSet, Name: "Set ARC limits now", Fields: fields,
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.ARCSet(ctx, v["max"], v["min"])
			},
		},
		{
			Key: "b", Op: mutate.ARCPersist, Name: "Keep ARC limits after reboot", Fields: append([]Field{}, fields...),
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.ARCPersist(ctx, v["max"], v["min"])
			},
		},
	}
}

// batchOffers are the changes that apply to several marked items at
// once; the others need one item and are not offered.
func batchOffers(b plan.Builder, t ui.Target, now func() time.Time) (string, []Offer) {
	names := t.Names
	if t.Kind != "snapshot" {
		return fmt.Sprintf("%d datasets", len(names)), []Offer{snapshotCreate(b, names, now)}
	}
	recursive := Field{Name: "recursive", Label: "Also the snapshots of the same name below (-r)", Default: "false", Switch: true}
	tag := Field{Name: "tag", Label: "Hold tag", Default: "keep"}
	return fmt.Sprintf("%d snapshots", len(names)), []Offer{
		{
			Key: "h", Op: mutate.SnapshotHold, Need: needs("hold"), Name: "Hold all", Fields: []Field{tag, recursive},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.Hold(ctx, names, v["tag"], v["recursive"] == "true")
			},
		},
		{
			Key: "u", Op: mutate.SnapshotRelease, Need: needs("release"), Name: "Release a hold on all", Fields: []Field{tag, recursive},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.Release(ctx, names, v["tag"], v["recursive"] == "true")
			},
		},
		{
			Key: "d", Op: mutate.SnapshotDestroy, Need: needs("destroy", "mount"), Name: "Destroy all", Fields: []Field{recursive},
			Build: func(ctx context.Context, v map[string]string) (*mutate.Plan, error) {
				return b.SnapshotDestroy(ctx, names, v["recursive"] == "true")
			},
		},
	}
}

// tuneOffers are the changes the Tune tab's findings point to, one per
// kind: recordsize (r) and compression (c) on a dataset, a module
// parameter (p), the ARC limits (s, the ARC's own form).
func tuneOffers(b plan.Builder, encoded []string) []Offer {
	var out []Offer
	for _, e := range encoded {
		c, ok := tune.DecodeChange(e)
		if !ok {
			continue
		}
		switch c.Kind {
		case "property":
			ds, prop, value := c.Dataset, c.Name, c.Value
			key := map[string]string{"recordsize": "r", "compression": "c"}[prop]
			if key == "" {
				continue
			}
			out = append(out, Offer{
				Key: key, Op: mutate.PropertySet, Need: needs(prop), Name: "Set " + prop + "=" + value + " on " + ds,
				Build: func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
					return b.PropertySet(ctx, ds, prop, value)
				},
			})
		case "param":
			name, value := c.Name, c.Value
			out = append(out, Offer{
				Key: "p", Op: mutate.ParamSet, Name: "Set " + name + " to " + value + " until reboot",
				Build: func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
					return b.ParamSet(ctx, name, value)
				},
			})
		case "arc":
			arc := arcOffers(b)
			if len(arc) > 0 {
				out = append(out, arc[0]) // set the limits now
			}
		}
	}
	return out
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// ZFSOffers returns the changes offered for ZFS selections.
func ZFSOffers(b plan.Builder, now func() time.Time) func(ui.Target) (string, []Offer) {
	return func(t ui.Target) (string, []Offer) {
		if t.Backend != "zfs" || len(t.Names) == 0 {
			return "", nil
		}
		n := t.Names[0]
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
			return n, poolOffers(b, n)
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
					return "cannot read the pool's state: " + err.Error()
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
	}
}

// deviceOffers are the changes to one device of a pool.
func deviceOffers(b plan.Builder, pool, dev string) []Offer {
	offer := func(key, name string, op mutate.Op) Offer {
		build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) {
			return b.DeviceChange(ctx, op, pool, dev)
		}
		return Offer{
			Key: key, Name: name, Op: op, Build: build,
			Check: func(ctx context.Context) string {
				p, err := build(ctx, nil)
				if err != nil {
					return err.Error()
				}
				return strings.Join(p.Blocked, "; ")
			},
		}
	}
	return []Offer{
		offer("o", "Bring online", mutate.DeviceOnline),
		offer("f", "Take offline", mutate.DeviceOffline),
		offer("c", "Clear its errors", mutate.DeviceClear),
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
				return "cannot read its encryption state: " + err.Error()
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

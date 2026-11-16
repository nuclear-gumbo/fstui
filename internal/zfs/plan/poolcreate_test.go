// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
)

const tib = 1 << 40

// inventory has four free disks (one smaller), one with an old pool's
// label, one mounted, and a partitioned system disk.
func inventory() []disks.Device {
	disk := func(n string, size uint64) disks.Device {
		return disks.Device{Name: n, Path: "/dev/" + n, ByID: "/dev/disk/by-id/ata-" + n, Size: size, Type: "disk", Serial: "S" + n}
	}
	old := disk("sde", 4*tib)
	old.FSType = "zfs_member"
	mounted := disk("sdf", 4*tib)
	mounted.Mountpoints = []string{"/srv"}
	sys := disk("sdg", tib)
	root := disks.Device{Name: "sdg1", Path: "/dev/sdg1", ByID: "/dev/disk/by-id/ata-sdg-part1", Size: tib, Type: "part", Parent: "sdg", FSType: "ext4", Mountpoints: []string{"/"}}
	return []disks.Device{disk("sda", 4*tib), disk("sdb", 4*tib), disk("sdc", 4*tib), disk("sdd", 2*tib), old, mounted, sys, root}
}

func creator(f *fakeReader, inv []disks.Device) Builder {
	return Builder{R: f, Devices: func(context.Context) ([]disks.Device, error) { return inv, nil }}
}

func id(n string) string { return "/dev/disk/by-id/ata-" + n }

func TestPoolCreateMirror(t *testing.T) {
	p, err := creator(&fakeReader{}, inventory()).PoolCreate(t.Context(), PoolSpec{
		Name: "tank", Layout: "mirror", Data: []string{id("sda"), id("sdb")}, Log: []string{id("sdc")},
		Ashift: "12", Compression: "lz4",
	})
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	want := "zpool create -o ashift=12 -O compression=lz4 -- tank mirror /dev/disk/by-id/ata-sda /dev/disk/by-id/ata-sdb log /dev/disk/by-id/ata-sdc"
	if got := p.Steps[0].String(); got != want {
		t.Errorf("command\n%s\nwant\n%s", got, want)
	}
	if mutate.TierOf(p) != mutate.High || p.Preview.Output.IsEmpty() {
		t.Errorf("tier %v, dry run %q", mutate.TierOf(p), p.Preview.Output.Raw())
	}
	if aff := p.Preview.Affects[0].Raw(); !strings.HasPrefix(aff, "about 4 TiB usable") {
		t.Errorf("capacity %q", aff)
	}
}

func TestPoolCreateRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		spec PoolSpec
		f    fakeReader
		why  string
	}{
		{"mounted", PoolSpec{Name: "tank", Data: []string{id("sdf")}}, fakeReader{}, "mounted at /srv"},
		{"partitioned", PoolSpec{Name: "tank", Data: []string{id("sdg")}}, fakeReader{}, "has partitions"},
		{"root partition", PoolSpec{Name: "tank", Data: []string{id("sdg-part1")}}, fakeReader{}, "mounted at /"},
		{"old label", PoolSpec{Name: "tank", Data: []string{id("sde")}}, fakeReader{}, "holds zfs_member; switch on overwrite"},
		{"unknown", PoolSpec{Name: "tank", Data: []string{"/dev/sdz"}}, fakeReader{}, "not a listed disk"},
		{"twice", PoolSpec{Name: "tank", Layout: "mirror", Data: []string{id("sda"), id("sda")}}, fakeReader{}, "chosen twice"},
		{"too few", PoolSpec{Name: "tank", Layout: "raidz2", Data: []string{id("sda"), id("sdb")}}, fakeReader{}, "at least 3 devices"},
		{"exists", PoolSpec{Name: "tank", Data: []string{id("sda")}}, fakeReader{names: []string{"tank"}}, "is imported"},
		{"one special", PoolSpec{Name: "tank", Data: []string{id("sda")}, Special: []string{id("sdb")}}, fakeReader{}, "single special"},
		{"zpool says no", PoolSpec{Name: "tank", Data: []string{id("sda")}}, fakeReader{create: errors.New("invalid vdev specification")}, "invalid vdev"},
	} {
		p, err := creator(&c.f, inventory()).PoolCreate(t.Context(), c.spec)
		if err != nil || !strings.Contains(strings.Join(p.Blocked, "; "), c.why) {
			t.Errorf("%s: blocked %q, %v; want %q", c.name, p.Blocked, err, c.why)
		}
	}
}

// Overwrite turns an old signature into something destroyed, with -f.
func TestPoolCreateOverwrite(t *testing.T) {
	p, err := creator(&fakeReader{}, inventory()).PoolCreate(t.Context(), PoolSpec{
		Name: "tank", Layout: "raidz1", Data: []string{id("sda"), id("sdd"), id("sde")}, Overwrite: true,
	})
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p.Blocked, err)
	}
	if !strings.HasPrefix(p.Steps[0].String(), "zpool create -f --") || len(p.Preview.Destroys) != 1 ||
		!strings.Contains(p.Preview.Destroys[0].Raw(), "zfs_member") {
		t.Errorf("%s, destroys %v", p.Steps[0], p.Preview.Destroys)
	}
	if aff := p.Preview.Affects[0].Raw(); !strings.Contains(aff, "about 4 TiB usable") || !strings.Contains(aff, "smallest device") {
		t.Errorf("capacity %q", aff)
	}
}

// A device that changed between the preview and running stops the plan.
func TestPoolCreateRecheck(t *testing.T) {
	inv := inventory()
	b := creator(&fakeReader{}, inv)
	p, err := b.PoolCreate(t.Context(), PoolSpec{Name: "tank", Data: []string{id("sda")}})
	if err != nil {
		t.Fatal(err)
	}
	inv[0].FSType = "ext4" // someone made a filesystem on it meanwhile
	now, err := p.Recheck(t.Context())
	if err != nil || len(p.Baseline.Diff(now)) == 0 {
		t.Errorf("recheck saw no change: %v", err)
	}
}

func TestPoolCreateSavedAndRebuilt(t *testing.T) {
	b := creator(&fakeReader{}, inventory())
	spec := PoolSpec{Name: "tank", Layout: "mirror", Data: []string{id("sda"), id("sdb")}, Autotrim: true, Mountpoint: "/tank"}
	p, err := b.PoolCreate(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.Build(t.Context(), mutate.PoolCreate, p.Params)
	if err != nil || again.Steps[0].String() != p.Steps[0].String() {
		t.Errorf("rebuilt %v, %v; was %v", again, err, p.Steps[0])
	}
	if _, err := (Builder{R: &fakeReader{}}).PoolCreate(t.Context(), spec); !errors.Is(err, ErrNoInventory) {
		t.Errorf("without lsblk: %v", err)
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs_test

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// TestIntegrationRealDisk uses the first device a person listed in
// testdata/allowed-devices.conf, and destroys what is on it. The disk is
// found through the same inventory as in fstui (lsblk, /dev/disk/by-id,
// /sys/class/block): a pool is created on the whole disk, the disk is then
// refused as in use, a checkpoint is rewound (the import searches
// /dev/disk/by-id), the pool is destroyed, and the label left on the
// partition is refused until overwrite is switched on.
//
// The disk must start empty; afterwards it holds a GPT and a destroyed
// pool's label, which `wipefs -a` on its partitions and then the disk
// removes (CONTRIBUTING.md).
func TestIntegrationRealDisk(t *testing.T) {
	b, pl, judge, dev := realDisk(t)
	pool := testpool.Images(t, 0)
	run := func(build func() (*mutate.Plan, error)) {
		t.Helper()
		runPlan(t, pl, build)
	}
	run(func() (*mutate.Plan, error) {
		return b.PoolCreate(t.Context(), plan.PoolSpec{Name: pool.Name, Data: []string{dev}, Mountpoint: testpool.Dir + "/mnt/" + pool.Name})
	})
	st, err := b.R.PoolTopology(t.Context(), pool.Name)
	if err != nil || len(st.Data.Children) != 1 || st.Data.Children[0].Path != dev+"-part1" {
		t.Fatalf("pool on %s: %+v, %v", dev, st.Data, err)
	}
	for _, d := range []string{dev, dev + "-part1"} {
		if v, why := judge(d); v != disks.Refused || !strings.Contains(why, pool.Name) {
			t.Errorf("%s while in a pool: %v %q", d, v, why)
		}
	}

	// A rewind exports the pool and imports it from /dev/disk/by-id.
	run(func() (*mutate.Plan, error) { return b.Checkpoint(t.Context(), mutate.CheckpointTake, pool.Name) })
	later := pool.CreateFilesystem("later")
	run(func() (*mutate.Plan, error) { return b.Checkpoint(t.Context(), mutate.CheckpointRewind, pool.Name) })
	if ids, err := b.R.Identify(t.Context(), []string{later}); err != nil || ids[later].GUID != "" {
		t.Errorf("%s after the rewind: %+v, %v", later, ids, err)
	}

	pool.Destroy()
	overwriteLabel(t, b, pl, judge, dev+"-part1", pool.Name)
}

// realDisk is the first allowed device, empty, with a builder whose
// device list is fstui's own (lsblk, /dev/disk/by-id, /sys/class/block),
// and judge, which looks a device up in a fresh list. It skips when no
// device is listed or the disk is not empty.
func realDisk(t *testing.T) (plan.Builder, *mutate.Pipeline, func(string) (disks.Verdict, string), string) {
	t.Helper()
	allowed := testpool.AllowedDevices(t)
	if len(allowed) == 0 {
		t.Skip("no device in testdata/allowed-devices.conf")
	}
	dev := allowed[0]
	b, pl, _, _ := writeSetup(t)
	tools, err := execute.Resolve(execute.Lsblk)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := b.R.(*zfs.Reader)
	if !ok {
		t.Fatalf("reader %T", b.R)
	}
	src := disks.Source{Run: execute.NewRunner(tools), ByIDDir: "/dev/disk/by-id", SysBlock: "/sys/class/block", PoolDevices: r.PoolDevices}
	b.Devices = src.Devices
	judge := func(path string) (disks.Verdict, string) {
		t.Helper()
		inv, err := src.Devices(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range inv {
			if d.ByID == path {
				return disks.Judge(d, inv)
			}
		}
		t.Fatalf("%s is not in the inventory", path)
		return 0, ""
	}
	if v, why := judge(dev); v != disks.Free {
		t.Skipf("%s is not empty (%s): scripts/integration.sh wipes it (CONTRIBUTING.md)", dev, why)
	}
	return b, pl, judge, dev
}

// overwriteLabel checks that a destroyed pool's label on part is refused
// until overwrite is on, and that zpool create -f then uses it.
func overwriteLabel(t *testing.T, b plan.Builder, pl *mutate.Pipeline, judge func(string) (disks.Verdict, string), part, pool string) {
	t.Helper()
	if v, why := judge(part); v != disks.Overwrite || !strings.Contains(why, "zfs_member") {
		t.Fatalf("%s after destroy: %v %q", part, v, why)
	}
	spec := plan.PoolSpec{Name: pool, Data: []string{part}, Mountpoint: testpool.Dir + "/mnt/" + pool}
	refused, err := b.PoolCreate(t.Context(), spec)
	if err != nil || !strings.Contains(strings.Join(refused.Blocked, ";"), "switch on overwrite") {
		t.Errorf("without overwrite: %v, %v", refused.Blocked, err)
	}
	spec.Overwrite = true
	forced, err := b.PoolCreate(t.Context(), spec)
	if err != nil || len(forced.Blocked) != 0 || !strings.Contains(forced.Steps[0].String(), "create -f ") || mutate.TierOf(forced) != mutate.High {
		t.Fatalf("with overwrite: %v, %v, %v", forced.Blocked, forced.Steps, err)
	}
	runPlan(t, pl, func() (*mutate.Plan, error) { return b.PoolCreate(t.Context(), spec) })
	if st, err := b.R.PoolTopology(t.Context(), pool); err != nil || st.Data.Children[0].Path != part {
		t.Fatalf("pool on %s: %+v, %v", part, st.Data, err)
	}
}

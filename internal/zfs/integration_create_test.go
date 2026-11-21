// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// fileDevices offers sparse files as devices: the only devices a test
// creates a pool on. A real block device is never offered.
func fileDevices(files []string) func(context.Context) ([]disks.Device, error) {
	return func(context.Context) ([]disks.Device, error) {
		var out []disks.Device
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil {
				return nil, err
			}
			out = append(out, disks.Device{Name: f, Path: f, ByID: f, Size: uint64(fi.Size()), Type: "disk"}) // a file size
		}
		return out, nil
	}
}

// A mirror made by the pipeline from the plan: dry run first, then the
// real zpool create, which the pool's existence and layout confirm.
func TestIntegrationPoolCreate(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	imgs := testpool.Images(t, 2)
	b.Devices = fileDevices(imgs.Images)
	spec := plan.PoolSpec{
		Name: imgs.Name, Layout: "mirror", Data: imgs.Images, Ashift: "12", Compression: "lz4",
		Mountpoint: testpool.Dir + "/mnt/" + imgs.Name,
	}
	p, err := b.PoolCreate(t.Context(), spec)
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %v, %v", p.Blocked, err)
	}
	if !strings.Contains(p.Preview.Output.Raw(), "mirror") {
		t.Errorf("dry run said %q", p.Preview.Output.Raw())
	}
	if _, err := pl.Run(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	st, err := b.R.PoolTopology(t.Context(), imgs.Name)
	if err != nil || len(st.Data.Children) != 1 || len(st.Data.Children[0].Children) != 2 {
		t.Fatalf("pool after create: %+v, %v", st, err)
	}
	// The same plan again is refused: the name is taken now.
	again, err := b.PoolCreate(t.Context(), spec)
	if err != nil || !strings.Contains(strings.Join(again.Blocked, ";"), "is imported") {
		t.Errorf("second create: %v, %v", again.Blocked, err)
	}
}

// Devices added to a mirror pool by the pipeline: a checkpoint, then a
// second mirror and a log device. A single device is refused before zpool
// is asked, and so is a device of the pool itself (by zpool's dry run).
func TestIntegrationPoolAdd(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	imgs := testpool.Images(t, 5)
	b.Devices = fileDevices(imgs.Images)
	created, err := b.PoolCreate(t.Context(), plan.PoolSpec{
		Name: imgs.Name, Layout: "mirror", Data: imgs.Images[:2], Mountpoint: testpool.Dir + "/mnt/" + imgs.Name,
	})
	if err != nil || len(created.Blocked) != 0 {
		t.Fatalf("create %v, %v", created.Blocked, err)
	}
	if _, err := pl.Run(t.Context(), created); err != nil {
		t.Fatal(err)
	}

	single, err := b.PoolAdd(t.Context(), plan.AddSpec{Pool: imgs.Name, Data: imgs.Images[2:3]})
	if err != nil || !strings.Contains(strings.Join(single.Blocked, ";"), "are mirror; adding a stripe") {
		t.Errorf("single device: %v, %v", single.Blocked, err)
	}
	member, err := b.PoolAdd(t.Context(), plan.AddSpec{Pool: imgs.Name, Log: imgs.Images[:1]})
	if err != nil || !strings.Contains(strings.Join(member.Blocked, ";"), "active pool") {
		t.Errorf("a device of the pool: %v, %v", member.Blocked, err)
	}

	p, err := b.PoolAdd(t.Context(), plan.AddSpec{
		Pool: imgs.Name, Layout: "mirror", Data: imgs.Images[2:4], Log: imgs.Images[4:], Checkpoint: true,
	})
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %v, %v", p.Blocked, err)
	}
	if !strings.Contains(p.Preview.Output.Raw(), "logs") {
		t.Errorf("dry run said %q", p.Preview.Output.Raw())
	}
	if _, err := pl.Run(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	st, err := b.R.PoolTopology(t.Context(), imgs.Name)
	if err != nil || len(st.Data.Children) != 2 || len(st.Logs) != 1 || st.Checkpoint == nil {
		t.Fatalf("pool after add: data %d, logs %d, checkpoint %v, %v", len(st.Data.Children), len(st.Logs), st.Checkpoint, err)
	}
}

// A checkpoint taken, a filesystem created after it, then the pool
// rewound: the filesystem is gone and so is the checkpoint. The import
// finds the sparse files through the -d the plan derives from them.
func TestIntegrationCheckpointRewind(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	imgs := testpool.Images(t, 2)
	b.Devices = fileDevices(imgs.Images)
	created, err := b.PoolCreate(t.Context(), plan.PoolSpec{
		Name: imgs.Name, Layout: "mirror", Data: imgs.Images, Mountpoint: testpool.Dir + "/mnt/" + imgs.Name,
	})
	if err != nil || len(created.Blocked) != 0 {
		t.Fatalf("create %v, %v", created.Blocked, err)
	}
	run := func(op mutate.Op) {
		t.Helper()
		runPlan(t, pl, func() (*mutate.Plan, error) { return b.Checkpoint(t.Context(), op, imgs.Name) })
	}
	if _, err := pl.Run(t.Context(), created); err != nil {
		t.Fatal(err)
	}
	run(mutate.CheckpointTake)
	time.Sleep(1100 * time.Millisecond) // creation has one-second resolution
	later := imgs.CreateFilesystem("later")
	exists := func() bool {
		t.Helper()
		ids, err := b.R.Identify(t.Context(), []string{later})
		if err != nil {
			t.Fatal(err)
		}
		return ids[later].GUID != ""
	}

	lookAndReturn(t, b, pl, imgs.Name, run, exists)

	rewind, err := b.Checkpoint(t.Context(), mutate.CheckpointRewind, imgs.Name)
	if err != nil || len(rewind.Blocked) != 0 || len(rewind.Preview.Destroys) != 1 || rewind.Preview.Destroys[0].Raw() != later {
		t.Fatalf("rewind plan: blocked %v, lost %v, %v", rewind.Blocked, rewind.Preview.Destroys, err)
	}
	if _, err := pl.Run(t.Context(), rewind); err != nil {
		t.Fatal(err)
	}
	st, err := b.R.PoolTopology(t.Context(), imgs.Name)
	if err != nil || st.Checkpoint != nil {
		t.Fatalf("after rewind: checkpoint %v, %v", st.Checkpoint, err)
	}
	if exists() {
		t.Errorf("%s exists after the rewind", later)
	}

	run(mutate.CheckpointTake)
	run(mutate.CheckpointDiscard)
}

// One device made a mirror by attaching a second, that second replaced by
// a third, then the third detached again, each through the pipeline. A
// smaller device is refused before zpool is asked.
func TestIntegrationAttachReplaceDetach(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	imgs := testpool.Images(t, 4)
	if err := os.Truncate(imgs.Images[3], 128<<20); err != nil {
		t.Fatal(err)
	}
	b.Devices = fileDevices(imgs.Images)
	created, err := b.PoolCreate(t.Context(), plan.PoolSpec{Name: imgs.Name, Data: imgs.Images[:1], Mountpoint: testpool.Dir + "/mnt/" + imgs.Name})
	if err != nil || len(created.Blocked) != 0 {
		t.Fatalf("create %v, %v", created.Blocked, err)
	}
	if _, err := pl.Run(t.Context(), created); err != nil {
		t.Fatal(err)
	}
	runNew := func(op mutate.Op, dev, newDev string) {
		t.Helper()
		p, err := b.DeviceNew(t.Context(), op, plan.NewDeviceSpec{Pool: imgs.Name, Device: dev, New: newDev})
		if err != nil || len(p.Blocked) != 0 {
			t.Fatalf("%s: %v, %v", op, p.Blocked, err)
		}
		if _, err := pl.Run(t.Context(), p); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		imgs.Wait("resilver,replace")
	}
	small, err := b.DeviceNew(t.Context(), mutate.DeviceAttach, plan.NewDeviceSpec{Pool: imgs.Name, Device: imgs.Images[0], New: imgs.Images[3]})
	if err != nil || !strings.Contains(strings.Join(small.Blocked, ";"), "smaller than") {
		t.Errorf("smaller device: %v, %v", small.Blocked, err)
	}
	runNew(mutate.DeviceAttach, imgs.Images[0], imgs.Images[1])
	runNew(mutate.DeviceReplace, imgs.Images[1], imgs.Images[2])
	st, err := b.R.PoolTopology(t.Context(), imgs.Name)
	if err != nil || len(st.Data.Children) != 1 || st.Data.Children[0].Type != "mirror" || st.Data.Children[0].Children[1].Path != imgs.Images[2] {
		t.Fatalf("after attach and replace: %+v, %v", st.Data, err)
	}
	p, err := b.DeviceChange(t.Context(), mutate.DeviceDetach, imgs.Name, imgs.Images[2])
	if err != nil || len(p.Blocked) != 0 || !p.Flags.RedundancyLoss {
		t.Fatalf("detach: %+v, %v", p, err)
	}
	if _, err := pl.Run(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if st, err = b.R.PoolTopology(t.Context(), imgs.Name); err != nil || len(st.Data.Children) != 1 || st.Data.Children[0].Path != imgs.Images[0] {
		t.Fatalf("after detach: %+v, %v", st.Data, err)
	}
}

// A raidz1 of three widened to four by attaching a device to one of its
// members; fstui names the raidz vdev to zpool.
func TestIntegrationRaidzExpand(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	imgs := testpool.Images(t, 4)
	b.Devices = fileDevices(imgs.Images)
	created, err := b.PoolCreate(t.Context(), plan.PoolSpec{
		Name: imgs.Name, Layout: "raidz1", Data: imgs.Images[:3], Mountpoint: testpool.Dir + "/mnt/" + imgs.Name,
	})
	if err != nil || len(created.Blocked) != 0 {
		t.Fatalf("create %v, %v", created.Blocked, err)
	}
	if _, err := pl.Run(t.Context(), created); err != nil {
		t.Fatal(err)
	}
	p, err := b.DeviceNew(t.Context(), mutate.DeviceAttach, plan.NewDeviceSpec{Pool: imgs.Name, Device: imgs.Images[0], New: imgs.Images[3]})
	if err != nil || len(p.Blocked) != 0 || !strings.Contains(p.Steps[0].String(), " raidz1-0 ") {
		t.Fatalf("plan %v, %v, %v", p.Blocked, p.Steps, err)
	}
	if _, err := pl.Run(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	imgs.Wait("raidz_expand")
	st, err := b.R.PoolTopology(t.Context(), imgs.Name)
	if err != nil || len(st.Data.Children[0].Children) != 4 || st.Expansion == nil || st.Expansion.State != "FINISHED" {
		t.Fatalf("after expansion: %+v, %v", st.Expansion, err)
	}
}

// Two mirrors, then the second removed through the pipeline, chosen by
// one of its members; afterwards one data vdev is left and the removal
// reads as finished.
func TestIntegrationVdevRemove(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	imgs := testpool.Images(t, 4)
	b.Devices = fileDevices(imgs.Images)
	created, err := b.PoolCreate(t.Context(), plan.PoolSpec{
		Name: imgs.Name, Layout: "mirror", Data: imgs.Images[:2], Mountpoint: testpool.Dir + "/mnt/" + imgs.Name,
	})
	if err != nil || len(created.Blocked) != 0 {
		t.Fatalf("create %v, %v", created.Blocked, err)
	}
	if _, err := pl.Run(t.Context(), created); err != nil {
		t.Fatal(err)
	}
	added, err := b.PoolAdd(t.Context(), plan.AddSpec{Pool: imgs.Name, Layout: "mirror", Data: imgs.Images[2:]})
	if err != nil || len(added.Blocked) != 0 {
		t.Fatalf("add %v, %v", added.Blocked, err)
	}
	if _, err := pl.Run(t.Context(), added); err != nil {
		t.Fatal(err)
	}
	p, err := b.VdevRemove(t.Context(), imgs.Name, imgs.Images[3])
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool remove -- "+imgs.Name+" mirror-1" || !strings.Contains(p.Preview.Output.Raw(), "Memory") {
		t.Fatalf("plan %v, %v, %q, %v", p.Blocked, p.Steps, p.Preview.Output.Raw(), err)
	}
	if _, err := pl.Run(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	imgs.Wait("remove")
	st, err := b.R.PoolTopology(t.Context(), imgs.Name)
	if err != nil || len(st.Data.Children) != 1 || st.Removal == nil || st.Removal.State != "FINISHED" {
		t.Fatalf("after removal: %d vdevs, %+v, %v", len(st.Data.Children), st.Removal, err)
	}
}

// A pool exported, found by zpool import in the test directory, imported
// read-only and unmounted under another name, exported again and
// imported as it was, each through the pipeline.
func TestIntegrationExportImport(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	imgs := testpool.Images(t, 2)
	b.Devices = fileDevices(imgs.Images)
	created, err := b.PoolCreate(t.Context(), plan.PoolSpec{Name: imgs.Name, Layout: "mirror", Data: imgs.Images, Mountpoint: testpool.Dir + "/mnt/" + imgs.Name})
	if err != nil || len(created.Blocked) != 0 {
		t.Fatalf("create %v, %v", created.Blocked, err)
	}
	run := func(p *mutate.Plan, err error) {
		t.Helper()
		if err != nil || len(p.Blocked) != 0 {
			t.Fatalf("%v, %v", p, err)
		}
		if _, err := pl.Run(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	run(created, nil)
	runPlan(t, pl, func() (*mutate.Plan, error) { return b.PoolExport(t.Context(), imgs.Name) })
	id := importableID(t, b, imgs.Name)
	ro := imgs.Name + "_ro"
	run(b.PoolImport(t.Context(), plan.ImportSpec{ID: id, NewName: ro, ReadOnly: true, NoMount: true, Dirs: []string{testpool.Dir}}))
	roName, err := name.ParsePool(ro)
	if err != nil {
		t.Fatal(err)
	}
	props, err := b.R.PoolProperties(t.Context(), roName)
	if err != nil || props.Properties["readonly"].Text.Raw() != "on" {
		t.Fatalf("%s readonly %q, %v", ro, props.Properties["readonly"].Text.Raw(), err)
	}
	runPlan(t, pl, func() (*mutate.Plan, error) { return b.PoolExport(t.Context(), ro) })
	run(b.PoolImport(t.Context(), plan.ImportSpec{ID: id, Dirs: []string{testpool.Dir}}))
	if _, err := b.R.PoolTopology(t.Context(), imgs.Name); err != nil {
		t.Fatalf("%s after import: %v", imgs.Name, err)
	}
}

// A pool created with every feature disabled is upgraded through the
// pipeline; one held back by compatibility=openzfs-2.1-linux is refused,
// although zpool upgrade without a pool lists features for it.
func TestIntegrationUpgrade(t *testing.T) {
	b, pl, _, _ := writeSetup(t)
	old := testpool.NewWith(t, []string{"-d"}, "", 1)
	p, err := b.PoolUpgrade(t.Context(), old.Name)
	if err != nil || len(p.Blocked) != 0 || !strings.Contains(p.Preview.Affects[0].Raw(), "features are enabled") {
		t.Fatalf("plan %v, %v, %v", p.Blocked, p.Preview.Affects, err)
	}
	if _, err := pl.Run(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	again, err := b.PoolUpgrade(t.Context(), old.Name)
	if err != nil || !strings.Contains(strings.Join(again.Blocked, ";"), "already") {
		t.Errorf("after upgrade: %v, %v", again.Blocked, err)
	}
	held := testpool.NewWith(t, []string{"-o", "compatibility=openzfs-2.1-linux"}, "", 1)
	p, err = b.PoolUpgrade(t.Context(), held.Name)
	if err != nil || !strings.Contains(strings.Join(p.Blocked, ";"), "allows none") {
		t.Errorf("compatibility: %v, %v", p.Blocked, err)
	}
}

// importableID is the id zpool import lists for the exported pool pool
// in the test directory.
func importableID(t *testing.T, b plan.Builder, pool string) string {
	t.Helper()
	found, err := b.R.Importable(t.Context(), []string{testpool.Dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if f.Name.Raw() == pool {
			return f.ID
		}
	}
	t.Fatalf("zpool import did not list %s: %+v", pool, found)
	return ""
}

// lookAndReturn looks at the pool's checkpoint (read-only, without what
// came after: exists is false), then returns to the present by export
// and import (exists is true again).
func lookAndReturn(t *testing.T, b plan.Builder, pl *mutate.Pipeline, pool string, run func(mutate.Op), exists func() bool) {
	t.Helper()
	run(mutate.CheckpointLook)
	if exists() {
		t.Fatal("what came after the checkpoint exists in the look at it")
	}
	ro, err := b.Checkpoint(t.Context(), mutate.CheckpointTake, pool)
	if err != nil || !strings.Contains(strings.Join(ro.Blocked, ";"), "read-only") {
		t.Errorf("checkpoint while looking: %v, %v", ro.Blocked, err)
	}
	runPlan(t, pl, func() (*mutate.Plan, error) { return b.PoolExport(t.Context(), pool) })
	imp, err := b.PoolImport(t.Context(), plan.ImportSpec{ID: importableID(t, b, pool), Dirs: []string{testpool.Dir}})
	if err != nil || len(imp.Blocked) != 0 {
		t.Fatalf("import: %v, %v", imp.Blocked, err)
	}
	if _, err := pl.Run(t.Context(), imp); err != nil {
		t.Fatal(err)
	}
	if !exists() {
		t.Fatal("what came after the checkpoint is missing after returning to the present")
	}
}

// runPlan plans and runs a change that must go through. A plan whose
// export finds the pool busy changed nothing; on the CI machine a new
// filesystem can stay busy for a moment after it is created, so that
// alone is planned and tried again, for up to five seconds.
func runPlan(t *testing.T, pl *mutate.Pipeline, build func() (*mutate.Plan, error)) {
	t.Helper()
	for try := 0; ; try++ {
		p, err := build()
		if err != nil || len(p.Blocked) != 0 {
			t.Fatalf("%v, %v", p, err)
		}
		_, err = pl.Run(t.Context(), p)
		if err == nil {
			return
		}
		if !strings.Contains(err.Error(), "pool is busy") || try == 20 {
			t.Fatalf("%s: %v", p.Summary, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

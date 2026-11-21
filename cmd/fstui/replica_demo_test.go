// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_nowrite

package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/ui"

	"github.com/nuclear-gumbo/fstui/internal/demo"
	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// replica is a demo with its write hooks and a planner over it.
type replica struct {
	t   *testing.T
	ctx context.Context
	r   *zfs.Reader
	b   plan.Builder
	h   writeHooks
}

func newReplica(t *testing.T) *replica {
	t.Helper()
	cmds := demo.New()
	sched, err := demo.NewSchedules()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sched.Close() })
	cmds.UseSchedules(sched)
	r := zfs.NewReader(cmds)
	r.CronFile = ""
	src := disks.Source{Run: cmds, PoolDevices: r.PoolDevices}
	return &replica{t: t, ctx: t.Context(), r: r, b: plan.Builder{R: r, Devices: src.Devices}, h: demoWrite(cmds, r, sched)}
}

// run carries out a plan the way the demo's write mode does.
func (x *replica) run(p *mutate.Plan, err error) {
	x.t.Helper()
	if err != nil {
		x.t.Fatal(err)
	}
	if len(p.Blocked) > 0 {
		x.t.Fatalf("%s blocked: %v", p.Summary, p.Blocked)
	}
	for _, s := range p.Steps {
		if _, err := x.h.simulate(s, nil); err != nil {
			x.t.Fatalf("%s: %s: %v", p.Summary, s, err)
		}
	}
}

// free are disks the demo offers for a new pool, n of them.
func (x *replica) free(n int) []string {
	x.t.Helper()
	inv, err := x.b.Devices(x.ctx)
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	for _, d := range inv {
		if v, _ := disks.Judge(d, inv); v == disks.Free && d.Type == "disk" && len(out) < n {
			out = append(out, d.ByID)
		}
	}
	if len(out) < n {
		x.t.Fatalf("only %d free disks: %v", len(out), out)
	}
	return out
}

func (x *replica) leaves(pool string) []string {
	x.t.Helper()
	st, err := x.r.PoolTopology(x.ctx, pool)
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	var walk func(v zfs.Vdev)
	walk = func(v zfs.Vdev) {
		if len(v.Children) == 0 && v.Type != "root" {
			out = append(out, v.Name.Raw())
		}
		for _, c := range v.Children {
			walk(c)
		}
	}
	walk(st.Data)
	return out
}

func (x *replica) pools() []string {
	x.t.Helper()
	names, err := x.r.PoolNames(x.ctx)
	if err != nil {
		x.t.Fatal(err)
	}
	return names
}

// The demo carries out every pool change a real build offers, and the
// views read the result as they would from zpool: the demo is a replica
// of the real program, not a picture of it.
func TestDemoPoolChanges(t *testing.T) {
	x := newReplica(t)
	x.createAndAdd()
	x.attachDetachReplace()
	x.exportImport()
	x.rewindAndRemove()
}

func (x *replica) createAndAdd() {
	t := x.t
	devs := x.free(4)
	x.run(x.b.PoolCreate(x.ctx, plan.PoolSpec{Name: "lab", Layout: "mirror", Data: devs[:2]}))
	if !slices.Contains(x.pools(), "lab") || len(x.leaves("lab")) != 2 {
		t.Fatalf("created: pools %v, lab %v", x.pools(), x.leaves("lab"))
	}
	if ds, err := x.r.Datasets(x.ctx); err != nil || !slices.ContainsFunc(ds, func(d zfs.Dataset) bool { return d.Name == "lab" }) {
		t.Errorf("lab's root filesystem: %v", err)
	}
	x.run(x.b.PoolAdd(x.ctx, plan.AddSpec{Pool: "lab", Layout: "mirror", Data: devs[2:4]}))
	if got := x.leaves("lab"); len(got) != 4 {
		t.Errorf("after add: %v", got)
	}
}

func (x *replica) attachDetachReplace() {
	t := x.t
	tankA := x.leaves("tank")[0]
	spare := x.free(1)[0]
	x.run(x.b.DeviceNew(x.ctx, mutate.DeviceAttach, plan.NewDeviceSpec{Pool: "tank", Device: tankA, New: spare}))
	if got := x.leaves("tank"); !slices.Contains(got, spare) || len(got) != 3 {
		t.Errorf("after attach: %v", got)
	}
	x.run(x.b.DeviceChange(x.ctx, mutate.DeviceDetach, "tank", spare))
	if got := x.leaves("tank"); slices.Contains(got, spare) {
		t.Errorf("after detach: %v", got)
	}
	x.run(x.b.DeviceNew(x.ctx, mutate.DeviceReplace, plan.NewDeviceSpec{Pool: "tank", Device: tankA, New: spare}))
	if got := x.leaves("tank"); !slices.Contains(got, spare) || slices.Contains(got, tankA) {
		t.Errorf("after replace: %v", got)
	}
}

func (x *replica) importable(name string) zfs.Importable {
	x.t.Helper()
	found, err := x.r.Importable(x.ctx, nil)
	if err != nil {
		x.t.Fatal(err)
	}
	i := slices.IndexFunc(found, func(f zfs.Importable) bool { return f.Name.Raw() == name })
	if i < 0 {
		x.t.Fatalf("%s is not importable: %+v", name, found)
	}
	return found[i]
}

func (x *replica) exportImport() {
	t := x.t
	x.run(x.b.PoolExport(x.ctx, "lab"))
	if slices.Contains(x.pools(), "lab") {
		t.Fatal("lab still imported after export")
	}
	x.run(x.b.PoolImport(x.ctx, plan.ImportSpec{ID: x.importable("lab").ID}))
	if !slices.Contains(x.pools(), "lab") || len(x.leaves("lab")) != 4 {
		t.Errorf("after import: %v %v", x.pools(), x.leaves("lab"))
	}
	x.run(x.b.PoolImport(x.ctx, plan.ImportSpec{ID: x.importable("oldtank").ID}))
	if st, err := x.r.PoolTopology(x.ctx, "oldtank"); err != nil || st.State != "DEGRADED" {
		t.Errorf("oldtank: %+v %v", st.State, err)
	}
}

func (x *replica) rewindAndRemove() {
	t := x.t
	// A look imports the pool read-only: the menu then offers only
	// export, saying how to return to the present.
	x.run(x.b.Checkpoint(x.ctx, mutate.CheckpointLook, "vault"))
	_, offers := x.h.offers(ui.Target{Backend: "zfs", Kind: "pool", Names: []string{"vault"}})
	for _, o := range offers {
		why := ""
		if o.Check != nil {
			why = o.Check(x.ctx)
		}
		readOnly := strings.Contains(why, "imported read-only")
		stays := o.Op == mutate.PoolExport || o.Op == mutate.PoolImport || o.Op == mutate.PoolCreate
		if stays == readOnly {
			t.Errorf("read-only vault: %s: %q", o.Name, why)
		}
		if o.Op == mutate.PoolExport {
			x.run(o.Build(x.ctx, nil))
		}
	}
	x.run(x.b.PoolImport(x.ctx, plan.ImportSpec{ID: x.importable("vault").ID}))
	x.run(x.b.Checkpoint(x.ctx, mutate.CheckpointRewind, "vault"))
	if st, err := x.r.PoolTopology(x.ctx, "vault"); err != nil || st.Checkpoint != nil || !slices.Contains(x.pools(), "vault") {
		t.Errorf("vault after rewind: %+v %v", st.Checkpoint, err)
	}
	st, err := x.r.PoolTopology(x.ctx, "fast")
	if err != nil || len(st.Cache) == 0 {
		t.Fatalf("fast has no cache device to remove: %v", err)
	}
	x.run(x.b.VdevRemove(x.ctx, "fast", st.Cache[0].Name.Raw()))
	if st, _ := x.r.PoolTopology(x.ctx, "fast"); len(st.Cache) != 0 {
		t.Errorf("cache still in fast: %+v", st.Cache)
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_nowrite

package demo

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/storage"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

type records struct{ n int }

func (r *records) Record(execute.StepEvent) error { r.n++; return nil }

// world is the demo with the real planners and pipeline on top, as the
// demo build wires them.
func world(t *testing.T) (plan.Builder, *mutate.Pipeline, *Commands) {
	t.Helper()
	c := New()
	arc, err := NewARC()
	if err != nil {
		t.Fatal(err)
	}
	c.UseARC(arc)
	mode := new(execute.Switch)
	mode.Set(execute.Write)
	pl := &mutate.Pipeline{Mode: mode, Mutator: execute.NewSimulatedMutator(mode, new(records), c.Apply)}
	r := zfs.NewReader(c)
	r.ReadFile = c.ReadFile
	return plan.Builder{R: r}, pl, c
}

func run(t *testing.T, pl *mutate.Pipeline, p *mutate.Plan, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) > 0 {
		t.Fatalf("%s blocked: %q", p.Summary, p.Blocked)
	}
	if rep, err := pl.Run(t.Context(), p); err != nil {
		t.Fatalf("%s: %+v, %v", p.Summary, rep, err)
	}
}

func exists(t *testing.T, b plan.Builder, names ...string) map[string]bool {
	t.Helper()
	ids, err := b.R.Identify(t.Context(), names)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, n := range names {
		_, out[n] = ids[n]
	}
	return out
}

func TestDemoSnapshotsAppearAndGo(t *testing.T) {
	b, pl, _ := world(t)
	ctx := t.Context()
	p, err := b.SnapshotCreate(ctx, []string{"tank/home"}, "try", true)
	run(t, pl, p, err)
	if got := exists(t, b, "tank/home@try", "tank/home/user@try"); !got["tank/home@try"] || !got["tank/home/user@try"] {
		t.Fatalf("after snapshot -r: %v", got)
	}
	// The views read the same state.
	snaps, err := b.R.(*zfs.Reader).AllSnapshots(ctx) //nolint:forcetypeassert // world builds it from a *zfs.Reader
	if err != nil || !strings.Contains(names(snaps), "tank/home@try") {
		t.Fatalf("AllSnapshots = %v, %v", names(snaps), err)
	}
	p, err = b.SnapshotDestroy(ctx, []string{"tank/home@try"}, true)
	run(t, pl, p, err)
	if got := exists(t, b, "tank/home@try", "tank/home/user@try"); got["tank/home@try"] || got["tank/home/user@try"] {
		t.Fatalf("after destroy -r: %v", got)
	}
}

func names(ds []zfs.Dataset) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return strings.Join(out, " ")
}

func TestDemoBlocksWhatZFSWouldRefuse(t *testing.T) {
	b, _, _ := world(t)
	ctx := t.Context()
	// tank/home@daily-1 is held (recorded tag keep) and has the clone tank/clone.
	p, err := b.SnapshotDestroy(ctx, []string{"tank/home@daily-1"}, false)
	if err != nil || len(p.Blocked) == 0 {
		t.Fatalf("destroy of a held, cloned snapshot: %q, %v", p.Blocked, err)
	}
	p, err = b.DatasetDestroy(ctx, "tank/home", false)
	if err != nil || len(p.Blocked) == 0 || !strings.Contains(strings.Join(p.Blocked, " "), "has children") {
		t.Fatalf("destroy of a dataset with children: %q, %v", p.Blocked, err)
	}
}

func TestDemoHoldsRenamesClonesAndProperties(t *testing.T) {
	b, pl, c := world(t)
	ctx := t.Context()
	h, err := b.Hold(ctx, []string{"tank/home@daily-2"}, "mine", false)
	run(t, pl, h, err)
	r, err := b.Release(ctx, []string{"tank/home@daily-2"}, "mine", false)
	run(t, pl, r, err)
	rn, err := b.SnapshotRename(ctx, "tank/home/user@daily-2", "weekly")
	run(t, pl, rn, err)
	bm, err := b.Bookmark(ctx, "tank/home/user@weekly", "w")
	run(t, pl, bm, err)
	cl, err := b.Clone(ctx, "tank/home/user@weekly", "tank/try")
	run(t, pl, cl, err)
	pr, err := b.Promote(ctx, "tank/try")
	run(t, pl, pr, err)
	cr, err := b.DatasetCreate(ctx, "tank/new", map[string]string{"compression": "zstd"})
	run(t, pl, cr, err)
	s, err := b.PropertySet(ctx, "tank/new", "quota", "1G")
	run(t, pl, s, err)
	in, err := b.PropertyInherit(ctx, "tank/new", "compression")
	run(t, pl, in, err)
	u, err := b.Unmount(ctx, "tank/new", false)
	run(t, pl, u, err)
	m, err := b.Mount(ctx, "tank/new")
	run(t, pl, m, err)
	dr, err := b.DatasetRename(ctx, "tank/new", "tank/newer")
	run(t, pl, dr, err)
	got := exists(t, b, "tank/home/user@weekly", "tank/home/user#w", "tank/try@weekly", "tank/newer", "tank/new")
	if !got["tank/home/user#w"] || !got["tank/try@weekly"] || !got["tank/newer"] || got["tank/new"] {
		t.Fatalf("after the changes: %v", got)
	}
	st, _ := c.load()
	if q := st.entries["tank/newer"].get("quota"); q != "1073741824" {
		t.Errorf("quota = %q", q)
	}
	if o := st.entries["tank/home/user"].get("origin"); o != "tank/try@weekly" {
		t.Errorf("after promote, tank/home/user's origin = %q", o)
	}
	d, err := b.DatasetDestroy(ctx, "tank/newer", true)
	run(t, pl, d, err)
}

func TestDemoRollback(t *testing.T) {
	b, pl, _ := world(t)
	ctx := t.Context()
	p, err := b.Rollback(ctx, "tank/home/user@daily-1", false)
	if err != nil || len(p.Blocked) == 0 {
		t.Fatalf("rollback past newer snapshots without -r: %q, %v", p.Blocked, err)
	}
	p, err = b.Rollback(ctx, "tank/home/user@daily-1", true)
	run(t, pl, p, err)
	if got := exists(t, b, "tank/home/user@daily-2"); got["tank/home/user@daily-2"] {
		t.Fatal("newer snapshot still there after rollback -r")
	}
}

func TestDemoApplyRefusesOtherCommands(t *testing.T) {
	_, err := New().Apply(execute.Argv{Tool: execute.Zpool, Options: []string{"destroy"}, Operands: []string{"tank"}}, nil)
	if err == nil {
		t.Fatal("zpool destroy was simulated")
	}
}

func TestDemoScrubAndClear(t *testing.T) {
	b, pl, c := world(t)
	ctx := t.Context()
	state := func() string {
		a, err := zfs.NewReader(c).PoolActivity(ctx, "tank")
		if err != nil {
			t.Fatal(err)
		}
		return a.ScanKind + " " + a.ScanState
	}
	for _, step := range []struct {
		op   mutate.Op
		want string
	}{
		{mutate.ScrubStart, "SCRUB running"},
		{mutate.ScrubPause, "SCRUB paused"},
		{mutate.ScrubResume, "SCRUB running"},
		{mutate.ScrubStop, "SCRUB canceled"},
	} {
		p, err := b.PoolChange(ctx, step.op, "tank")
		run(t, pl, p, err)
		if got := state(); got != step.want {
			t.Fatalf("after %s: %q, want %q", step.op, got, step.want)
		}
	}
	p, err := b.PoolChange(ctx, mutate.ScrubPause, "tank")
	if err != nil || len(p.Blocked) == 0 {
		t.Fatalf("pause with no scrub: %q, %v", p.Blocked, err)
	}
	p, err = b.PoolChange(ctx, mutate.PoolClear, "scratch")
	run(t, pl, p, err)
	st, err := zfs.NewReader(c).PoolStatus(ctx, storage.Pool{Name: termtext.New("scratch")})
	if err != nil {
		t.Fatal(err)
	}
	var errs uint64
	var walk func([]storage.Device)
	walk = func(ds []storage.Device) {
		for _, d := range ds {
			errs += d.ReadErrors + d.WriteErrors + d.ChecksumErrors
			walk(d.Children)
		}
	}
	walk(st.Devices)
	if errs != 0 {
		t.Fatalf("errors after clear: %d", errs)
	}
}

func TestDemoDeviceOfflineAndOnline(t *testing.T) {
	b, pl, c := world(t)
	ctx := t.Context()
	st, err := zfs.NewReader(c).PoolTopology(ctx, "tank")
	if err != nil || len(st.Data.Children) == 0 || len(st.Data.Children[0].Children) < 2 {
		t.Fatalf("tank's topology: %+v, %v", st.Data, err)
	}
	dev := st.Data.Children[0].Children[0].Name.Raw()
	off, err := b.DeviceChange(ctx, mutate.DeviceOffline, "tank", dev)
	run(t, pl, off, err)
	st, _ = zfs.NewReader(c).PoolTopology(ctx, "tank")
	if st.State != "DEGRADED" || st.Data.Children[0].Children[0].State != "OFFLINE" {
		t.Fatalf("after offline: pool %s, device %s", st.State, st.Data.Children[0].Children[0].State)
	}
	on, err := b.DeviceChange(ctx, mutate.DeviceOnline, "tank", dev)
	run(t, pl, on, err)
	st, _ = zfs.NewReader(c).PoolTopology(ctx, "tank")
	if st.State != "ONLINE" || st.Data.Children[0].Children[0].State != "ONLINE" {
		t.Fatalf("after online: pool %s, device %s", st.State, st.Data.Children[0].Children[0].State)
	}
}

func TestDemoVolumes(t *testing.T) {
	b, pl, c := world(t)
	ctx := t.Context()
	cr, err := b.VolumeCreate(ctx, "tank/vm", "4G", true, "")
	run(t, pl, cr, err)
	vols, err := zfs.NewReader(c).AllVolumes(ctx)
	if err != nil || !strings.Contains(names(vols), "tank/vm") {
		t.Fatalf("volumes after create: %s, %v", names(vols), err)
	}
	grow, err := b.VolumeResize(ctx, "tank/vm", "8G")
	run(t, pl, grow, err)
	st, _ := c.load()
	if v := st.entries["tank/vm"].get("volsize"); v != "8589934592" {
		t.Fatalf("volsize after grow: %q", v)
	}
}

func TestDemoEncryption(t *testing.T) {
	b, pl, c := world(t)
	ctx := t.Context()
	cr, err := b.EncryptedCreate(ctx, "tank/vault", nil, []byte("correct horse"))
	run(t, pl, cr, err)
	un, err := b.KeyUnload(ctx, "tank/vault")
	run(t, pl, un, err)
	st, _ := c.load()
	if e := st.entries["tank/vault"]; e.get("keystatus") != "unavailable" || e.get("mounted") != "no" {
		t.Fatalf("after unload: %s, mounted %s", e.get("keystatus"), e.get("mounted"))
	}
	load, err := b.KeyLoad(ctx, "tank/vault", []byte("correct horse"))
	run(t, pl, load, err)
	if e := st.entries["tank/vault"]; e.get("keystatus") != "available" || e.get("mounted") != "yes" {
		t.Fatalf("after load: %s, mounted %s", e.get("keystatus"), e.get("mounted"))
	}
}

func TestDemoARCLimits(t *testing.T) {
	b, pl, c := world(t)
	ctx := t.Context()
	set, err := b.ARCSet(ctx, "4G", "")
	run(t, pl, set, err)
	if s, _ := c.arc.Read(); s.Max != 4<<30 {
		t.Fatalf("ARC max after set: %d", s.Max)
	}
	keep, err := b.ARCPersist(ctx, "4G", "")
	run(t, pl, keep, err)
	conf, ok, err := b.R.Modprobe()
	if err != nil || !ok || string(conf) != "options zfs zfs_arc_max=4294967296\n" {
		t.Fatalf("modprobe file after keep: %q, %v, %v", conf, ok, err)
	}
	again, err := b.ARCSet(ctx, "0", "")
	if err != nil || len(again.Blocked) == 0 {
		t.Fatalf("back to automatic: %q, %v", again.Blocked, err)
	}
}

func TestDemoDiff(t *testing.T) {
	_, _, c := world(t)
	got, _, err := zfs.NewReader(c).Diff(t.Context(), "tank/home@daily-1", "")
	if err != nil || len(got) != 6 || got[4].Change != 'R' || !strings.HasSuffix(got[4].NewPath.Raw(), "/photos/beach.jpg") {
		t.Fatalf("diff = %+v, %v", got, err)
	}
}

// The demo answers send size estimates in zfs's format: a full stream,
// increments between snapshots, and a missing snapshot as zfs refuses it.
func TestDemoSendEstimate(t *testing.T) {
	b, _, _ := world(t)
	r, ok := b.R.(*zfs.Reader)
	if !ok {
		t.Fatalf("reader is %T", b.R)
	}
	full, err := r.EstimateSend(t.Context(), zfs.SendStep{To: "tank/home@daily-1"}, zfs.SendOptions{})
	if err != nil || full.Total == 0 || len(full.Streams) != 1 {
		t.Fatalf("full: %+v, %v", full, err)
	}
	comp, err := r.EstimateSend(t.Context(), zfs.SendStep{To: "tank/home@daily-1"}, zfs.SendOptions{Compressed: true})
	if err != nil || comp.Total > full.Total {
		t.Errorf("compressed %d is larger than %d (%v)", comp.Total, full.Total, err)
	}
	inc, err := r.EstimateSend(t.Context(), zfs.SendStep{From: "tank/home@daily-1", To: "tank/home@daily-2"}, zfs.SendOptions{})
	if err != nil || len(inc.Streams) != 1 || inc.Streams[0].From != "tank/home@daily-1" {
		t.Errorf("incremental: %+v, %v", inc, err)
	}
	if _, err := r.EstimateSend(t.Context(), zfs.SendStep{To: "tank/home@nope"}, zfs.SendOptions{}); err == nil {
		t.Error("a missing snapshot was estimated")
	}
}

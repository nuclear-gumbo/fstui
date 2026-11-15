// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/storage"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// The real reader, parsers included, works against the replay: every
// read the views make is answered, under the demo's names.
func TestReaderOnReplay(t *testing.T) {
	r := zfs.NewReader(New())
	t.Run("pools", func(t *testing.T) { checkPools(t, r) })
	t.Run("history", func(t *testing.T) { checkHistory(t, r) })
	t.Run("datasets", func(t *testing.T) { checkDatasets(t, r) })
}

func checkPools(t *testing.T, r *zfs.Reader) {
	ctx := context.Background()
	ov, err := r.Overviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range ov {
		names = append(names, o.Pool.Name.String())
		if o.Used == 0 {
			t.Errorf("%s: no root dataset figures", o.Pool.Name)
		}
	}
	if got := strings.Join(names, ","); got != "archive,backup,fast,media,scratch,tank,vault" {
		t.Fatalf("pools %s", got)
	}
	if p, err := r.PoolProperties(ctx, mustPool(t, "tank")); err != nil || p.Properties["guid"].Text.Raw() == "" {
		t.Errorf("pool properties: %v", err)
	}
	if st, err := r.PoolStatus(ctx, storage.Pool{Name: termtext.New("backup")}); err != nil || st.Health.OK() {
		t.Errorf("backup should be degraded: %+v, %v", st.Health, err)
	}
	for _, o := range ov {
		for _, v := range o.Status.Data.Children {
			for _, d := range append([]zfs.Vdev{v}, v.Children...) {
				if n := d.Name.Display(); leaked(n) || strings.Contains(n, "FSTUITEST") {
					t.Errorf("%s: recorded device name %s", o.Pool.Name, n)
				}
			}
		}
	}
}

func checkHistory(t *testing.T, r *zfs.Reader) {
	h, err := r.PoolHistory(context.Background(), mustPool(t, "tank"))
	if err != nil || len(h) == 0 {
		t.Fatalf("history: %d entries, %v", len(h), err)
	}
	created := false
	for _, e := range h {
		text := e.Text.Display()
		if leaked(text) {
			t.Errorf("recorded name leaked into history: %s", text)
		}
		created = created || strings.Contains(text, "zpool create -O mountpoint=/tank") && strings.Contains(text, "/dev/disk/by-id/ata-DEMO_TANK_A")
	}
	if !created {
		t.Error("history lacks the renamed zpool create")
	}
}

func leaked(s string) bool {
	return strings.Contains(s, "fstuitest") || strings.Contains(s, "fstui-test")
}

func checkDatasets(t *testing.T, r *zfs.Reader) {
	ctx := context.Background()
	all, err := r.Datasets(ctx)
	if err != nil || len(all) < 10 {
		t.Fatalf("datasets: %d, %v", len(all), err)
	}
	mounts := map[string]string{}
	for _, d := range all {
		mounts[d.Name] = d.Properties["mountpoint"].Text.Raw()
		if leaked(d.Name) || leaked(mounts[d.Name]) {
			t.Errorf("recorded name leaked: %s %s", d.Name, mounts[d.Name])
		}
	}
	if mounts["tank/home"] != "/tank/home" {
		t.Errorf("tank/home mounted at %q", mounts["tank/home"])
	}
	home := mustDataset(t, "tank/home")
	if pd, err := r.PoolDatasets(ctx, mustPool(t, "tank")); err != nil || len(pd) == 0 {
		t.Errorf("pool datasets: %d, %v", len(pd), err)
	}
	if ds, err := r.DatasetProperties(ctx, home); err != nil || ds.Name != "tank/home" {
		t.Errorf("dataset properties: %v", err)
	}
	if s, err := r.DatasetSnapshots(ctx, home); err != nil || len(s) == 0 {
		t.Errorf("dataset snapshots: %d, %v", len(s), err)
	}
	if s, err := r.AllSnapshots(ctx); err != nil || len(s) == 0 {
		t.Errorf("all snapshots: %d, %v", len(s), err)
	}
	if v, err := r.AllVolumes(ctx); err != nil || len(v) == 0 {
		t.Errorf("volumes: %d, %v", len(v), err)
	}
}

// zpool iostat yields an interval a second for every pool, with the
// activity each pool's story implies.
func TestIOStream(t *testing.T) {
	r := zfs.NewReader(New())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var got []zfs.IOSample
	err := r.IOStats(ctx, func(s zfs.IOSample) {
		got = append(got, s)
		if len(got) == 2 {
			cancel()
		}
	})
	if err != nil || len(got) < 2 {
		t.Fatalf("%d samples, %v", len(got), err)
	}
	byName := map[string]zfs.IOPool{}
	for _, p := range got[1] {
		byName[p.Name.Display()] = p
	}
	if len(byName) != 7 {
		t.Fatalf("%d pools", len(byName))
	}
	if m := byName["media"]; m.ReadBytes < 20<<20 || len(m.Devices) == 0 {
		t.Errorf("media is being scrubbed but reads %d", m.ReadBytes)
	}
	if got := sections(byName["fast"]); got != "special,logs,cache" {
		t.Errorf("fast sections %s", got)
	}
}

// sections lists the role sections of a pool's devices in order.
func sections(p zfs.IOPool) string {
	var out []string
	for _, d := range p.Devices {
		if d.Section != "" && (len(out) == 0 || out[len(out)-1] != d.Section) {
			out = append(out, d.Section)
		}
	}
	return strings.Join(out, ",")
}

func TestARCMoves(t *testing.T) {
	a, err := NewARC()
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := a.Read()
	s2, _ := a.Read()
	r, ok := zfs.HitRatio(s1, s2)
	if !ok || r < 0.85 || r > 0.996 || s2.L2Devices != 1 || s2.Size < s2.Min || s2.Size > s2.Max {
		t.Errorf("ratio %v ok %v, l2 %d, size %d", r, ok, s2.L2Devices, s2.Size)
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, err := New().Read(context.Background(), zfsArgv("destroy", "tank")); err == nil || !strings.Contains(err.Error(), "no recording") {
		t.Errorf("err = %v", err)
	}
}

func mustPool(t *testing.T, s string) name.Pool {
	t.Helper()
	p, err := name.ParsePool(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustDataset(t *testing.T, s string) name.Dataset {
	t.Helper()
	d, err := name.ParseDataset(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func zfsArgv(opt, operand string) execute.Argv {
	return execute.Argv{Tool: execute.ZFS, Options: []string{opt}, Operands: []string{operand}}
}

// The recorded events read as tank's, with demo device names.
func TestEventsReplay(t *testing.T) {
	evs, _, err := zfs.NewReader(New()).Events(context.Background())
	if err != nil || len(evs) == 0 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	for _, e := range evs {
		if e.Source.Raw() != "tank" || leaked(e.Device.Raw()) || leaked(e.Summary.Raw()) {
			t.Fatalf("event %s on %q device %q: %q", e.Class.Raw(), e.Source.Raw(), e.Device.Raw(), e.Summary.Raw())
		}
	}
}

// Delegations are replayed for tank/home/user, renamed.
func TestAllowReplay(t *testing.T) {
	r := zfs.NewReader(New())
	d, err := r.Allow(context.Background(), mustDataset(t, "tank/home/user"))
	if err != nil || len(d) != 3 || d[0].Dataset.Raw() != "tank/home/user" || d[2].Dataset.Raw() != "tank" {
		t.Fatalf("%+v, %v", d, err)
	}
	if d, err := r.Allow(context.Background(), mustDataset(t, "tank")); err != nil || len(d) != 0 {
		t.Errorf("tank: %+v, %v", d, err)
	}
}

// Sizes look like disks, not test files: the recorded 512 MiB files are
// 4 TiB, while percentages and block sizes stay as recorded.
func TestScaledSizes(t *testing.T) {
	ctx := context.Background()
	r := zfs.NewReader(New())
	ov, err := r.Overviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ov {
		size, _ := o.Pool.Properties.Number("size")
		capacity, _ := o.Pool.Properties.Number("capacity")
		if size < 1<<40 || capacity > 100 {
			t.Errorf("%s: size %d, capacity %d%%", o.Pool.Name, size, capacity)
		}
	}
	d, err := r.DatasetProperties(ctx, mustDataset(t, "tank/home"))
	if err != nil {
		t.Fatal(err)
	}
	if rs, _ := d.Properties.Number("recordsize"); rs != 128<<10 {
		t.Errorf("recordsize %d was scaled", rs)
	}
	if used, _ := d.Properties.Number("used"); used < 1<<30 {
		t.Errorf("tank/home uses %d bytes", used)
	}
	a, err := r.Allow(ctx, mustDataset(t, "tank/home/user"))
	if err != nil || len(a) < 2 || len(a[1].Grants) == 0 {
		t.Fatalf("%+v, %v", a, err)
	}
}

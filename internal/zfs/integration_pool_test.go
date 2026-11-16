// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration && !fstui_nowrite

package zfs_test

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

// fill writes n MiB that compress to nothing less, so a scrub has work.
func fill(t *testing.T, p *testpool.Pool, mib int) {
	t.Helper()
	f, err := os.Create(filepath.Join(testpool.Dir, "mnt", p.Name, "fill"))
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // test data, not secrets
	buf := make([]byte, 1<<20)
	for range mib {
		for i := range buf {
			buf[i] = byte(r.Uint32())
		}
		if _, err := f.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	p.Sync()
}

func TestIntegrationScrubControl(t *testing.T) {
	p := testpool.New(t, "", 1)
	fill(t, p, 300)
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	start, err := b.PoolChange(ctx, mutate.ScrubStart, p.Name)
	mustRun(t, pl, start, err)
	pause, err := b.PoolChange(ctx, mutate.ScrubPause, p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(pause.Blocked) > 0 {
		t.Skipf("the scrub finished before it could be paused (%q); this machine is too fast for the test data", pause.Blocked)
	}
	mustRun(t, pl, pause, err)
	a, err := b.R.PoolActivity(ctx, p.Name)
	if err != nil || a.ScanState != "paused" {
		t.Fatalf("after pause: %+v, %v", a, err)
	}
	again, err := b.PoolChange(ctx, mutate.ScrubStart, p.Name)
	if err != nil || len(again.Blocked) == 0 {
		t.Fatalf("start while paused: %q, %v", again.Blocked, err)
	}
	stop, err := b.PoolChange(ctx, mutate.ScrubStop, p.Name)
	mustRun(t, pl, stop, err)
	if a, err := b.R.PoolActivity(ctx, p.Name); err != nil || a.ScanState != "canceled" {
		t.Fatalf("after stop: %+v, %v", a, err)
	}
}

func TestIntegrationTrimInitializeClear(t *testing.T) {
	p := testpool.New(t, "", 1)
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	// Each change waits for the one before to end: the re-check before a
	// change compares the devices' trim and initialize states, and a
	// trim that finishes in between rightly refuses the next one.
	waitFor := map[mutate.Op]string{mutate.ErrorScrub: "scrub", mutate.TrimStart: "trim", mutate.InitializeStart: "initialize"}
	for _, op := range []mutate.Op{mutate.ErrorScrub, mutate.TrimStart, mutate.InitializeStart, mutate.PoolClear} {
		pn, err := b.PoolChange(ctx, op, p.Name)
		mustRun(t, pl, pn, err)
		if w := waitFor[op]; w != "" {
			p.Wait(w)
		}
	}
	a, err := b.R.PoolActivity(ctx, p.Name)
	if err != nil || len(a.Devices) != 1 || a.Devices[0].Init == "" {
		t.Fatalf("activity after initialize: %+v, %v", a, err)
	}
}

func TestIntegrationDeviceOfflineOnlineClear(t *testing.T) {
	p := testpool.New(t, "mirror", 2)
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	off, err := b.DeviceChange(ctx, mutate.DeviceOffline, p.Name, p.Images[0])
	if err != nil || mutate.TierOf(off) != mutate.High {
		t.Fatalf("offline one side of a two-way mirror: tier %s, %v", mutate.TierOf(off), err)
	}
	mustRun(t, pl, off, err)
	other, err := b.DeviceChange(ctx, mutate.DeviceOffline, p.Name, p.Images[1])
	if err != nil || len(other.Blocked) == 0 {
		t.Fatalf("offline the last side: blocked %q, %v", other.Blocked, err)
	}
	on, err := b.DeviceChange(ctx, mutate.DeviceOnline, p.Name, p.Images[0])
	mustRun(t, pl, on, err)
	clr, err := b.DeviceChange(ctx, mutate.DeviceClear, p.Name, p.Images[1])
	mustRun(t, pl, clr, err)
	st, err := b.R.PoolTopology(ctx, p.Name)
	if err != nil || st.State != "ONLINE" {
		t.Fatalf("after online: %s, %v", st.State, err)
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func poolWorld(a zfs.Activity) Builder {
	r := world()
	a.GUID = "77"
	r.pools = map[string]zfs.Activity{"tank": a}
	return Builder{R: r}
}

func TestPoolChangesBlockWhereZpoolWouldRefuse(t *testing.T) {
	idle := zfs.Activity{ScanKind: "SCRUB", ScanState: "finished", Devices: []zfs.DeviceActivity{{Name: "sda", Trim: "UNTRIMMED"}}}
	running := idle
	running.ScanState = "running"
	paused := idle
	paused.ScanState = "paused"
	resilver := zfs.Activity{ScanKind: "RESILVER", ScanState: "running"}
	tests := []struct {
		op      mutate.Op
		a       zfs.Activity
		blocked string // "" for not blocked
	}{
		{mutate.ScrubStart, idle, ""},
		{mutate.ScrubStart, running, "is running"},
		{mutate.ScrubStart, paused, "resume it"},
		{mutate.ScrubStart, resilver, "resilver is running"},
		{mutate.ScrubPause, idle, "no scrub is running"},
		{mutate.ScrubPause, running, ""},
		{mutate.ScrubResume, paused, ""},
		{mutate.ScrubResume, running, "no scrub is paused"},
		{mutate.ScrubStop, paused, ""},
		{mutate.ScrubStop, idle, "no scrub"},
		{mutate.ErrorScrub, running, "is running"},
		{mutate.TrimStart, idle, ""},
		{mutate.TrimStart, zfs.Activity{Devices: []zfs.DeviceActivity{{Name: "f", TrimNotSupp: true}}}, "supports trim"},
		{mutate.InitializeStart, zfs.Activity{Devices: []zfs.DeviceActivity{{Name: "f", Init: "ACTIVE"}}}, "already running"},
		{mutate.PoolClear, running, ""},
	}
	for _, tt := range tests {
		p, err := poolWorld(tt.a).PoolChange(t.Context(), tt.op, "tank")
		if err != nil {
			t.Fatalf("%s: %v", tt.op, err)
		}
		got := strings.Join(p.Blocked, "; ")
		if tt.blocked == "" && got != "" || tt.blocked != "" && !strings.Contains(got, tt.blocked) {
			t.Errorf("%s on %s %s: blocked %q, want %q", tt.op, tt.a.ScanKind, tt.a.ScanState, got, tt.blocked)
		}
	}
}

func TestPoolChangeCommandsAndTiers(t *testing.T) {
	b := poolWorld(zfs.Activity{ScanKind: "SCRUB", ScanState: "running"})
	stop, err := b.PoolChange(t.Context(), mutate.ScrubStop, "tank")
	if err != nil || stop.Steps[0].String() != "zpool scrub -s -- tank" || mutate.TierOf(stop) != mutate.Medium || len(stop.Irreversible) == 0 {
		t.Fatalf("stop = %+v, %v", stop, err)
	}
	pause, _ := b.PoolChange(t.Context(), mutate.ScrubPause, "tank")
	if pause.Steps[0].String() != "zpool scrub -p -- tank" || mutate.TierOf(pause) != mutate.Low {
		t.Errorf("pause = %+v", pause)
	}
	q, err := b.Build(t.Context(), mutate.ScrubPause, pause.Params)
	if err != nil || q.Summary != pause.Summary {
		t.Errorf("rebuilt %+v, %v", q, err)
	}
	b.Unprivileged = true
	if p, _ := b.PoolChange(t.Context(), mutate.PoolClear, "tank"); len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "need root") {
		t.Errorf("unprivileged clear: %q", p.Blocked)
	}
}

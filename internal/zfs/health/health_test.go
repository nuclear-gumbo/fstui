// SPDX-License-Identifier: GPL-3.0-or-later

package health

import (
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/problem"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

var th = config.Thresholds{CapacityWarn: 80, CapacityError: 90, FragmentationWarn: 50, ScrubOverdueDays: 35, CheckpointAge: 24 * time.Hour}

func pool(t *testing.T, n string, used, avail uint64) zfs.Overview {
	t.Helper()
	p, err := name.ParsePool(n)
	if err != nil {
		t.Fatal(err)
	}
	return zfs.Overview{Pool: zfs.Pool{Name: p}, Status: zfs.PoolStatus{Name: p, State: "ONLINE"}, Used: used, Available: avail}
}

func byKey(ps []problem.Problem) map[string]problem.Problem {
	m := map[string]problem.Problem{}
	for _, p := range ps {
		m[p.Key] = p
	}
	return m
}

// Each problem has its key, severity and the state that makes it new.
func TestCheck(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	full := pool(t, "tank", 95, 5)
	full.Status.State, full.Status.ErrorCount = "DEGRADED", 3
	full.Status.Checkpoint = &zfs.Checkpoint{Taken: now.Add(-48 * time.Hour), Space: 1 << 30}
	full.Status.Scan = &zfs.Scan{Function: "SCRUB", End: now.Add(-40 * 24 * time.Hour)}
	warm := pool(t, "media", 82, 18)
	quiet := pool(t, "fast", 10, 90)
	got := byKey(Check([]zfs.Overview{full, warm, quiet}, Options{
		Thresholds: th, Now: now, Version: zfs.Version{Userland: "zfs-2.3.9", Kernel: "zfs-kmod-2.3.8", UserlandRelease: zfs.Release{Major: 2, Minor: 3, Patch: 9}, KernelRelease: zfs.Release{Major: 2, Minor: 3, Patch: 8}},
		ScrubNote: func(string) string { return "; no scrub is scheduled" },
	}))
	for key, want := range map[string]struct {
		sev      problem.Severity
		state    string
		advisory bool
		text     string
	}{
		"health:tank":     {problem.Error, "DEGRADED", false, "tank is DEGRADED"},
		"data:tank":       {problem.Error, "", false, "3 data errors"},
		"capacity:tank":   {problem.Error, "FULL", true, "95% full"},
		"capacity:media":  {problem.Warning, "WARN", true, "82% full"},
		"checkpoint:tank": {problem.Warning, now.Add(-48 * time.Hour).String(), true, "held a checkpoint for 2d (1.0 GiB)"},
		"scrub:tank":      {problem.Warning, now.Add(-40 * 24 * time.Hour).String(), true, "40d ago (overdue after 35 days); no scrub is scheduled"},
		"version":         {problem.Warning, "zfs-2.3.9 zfs-kmod-2.3.8", true, "Reboot or reload the module"},
	} {
		p, ok := got[key]
		if !ok {
			t.Errorf("%s: missing", key)
			continue
		}
		if p.Severity != want.sev || p.State != want.state || p.Advisory != want.advisory || !strings.Contains(p.Text, want.text) {
			t.Errorf("%s: %+v", key, p)
		}
	}
	if len(got) != 7 {
		t.Errorf("%d problems: %v", len(got), got)
	}
}

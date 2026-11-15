// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// With Debian's cron file as shipped, a new pool is scrubbed on the
// second Sunday unless its root dataset sets org.debian:periodic-scrub
// to disable. systemctl runs for real; the test machine is expected to have no scrub
// timers.
func TestIntegrationScrubSchedules(t *testing.T) {
	tp := testpool.New(t, "", 1)
	tools, err := execute.Resolve(execute.ZFS, execute.Zpool, execute.Systemctl)
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(execute.NewRunner(tools))
	// The binary runs from the repository root, not this package, so the
	// cron file is written here rather than read from testdata.
	r.CronFile = filepath.Join(t.TempDir(), "zfsutils-linux")
	if err := os.WriteFile(r.CronFile, []byte(debianScrubLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ov := []Overview{{Pool: Pool{Name: mustPoolName(t, tp.Name)}, Status: PoolStatus{State: "ONLINE"}}}

	got, err := r.ScrubSchedules(t.Context(), ov)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got[tp.Name]
	if !ok || s.Next.IsZero() || s.Next.Weekday() != time.Sunday || s.Next.Day() < 8 || s.Next.Day() > 14 {
		t.Fatalf("schedule %+v (found %v)", s, ok)
	}

	tp.SetProperty("", DebianScrubProperty, "disable")
	if got, err := r.ScrubSchedules(t.Context(), ov); err != nil || len(got) != 0 {
		t.Errorf("with disable: %+v, %v", got, err)
	}
}

func mustPoolName(t *testing.T, s string) name.Pool {
	t.Helper()
	p, err := name.ParsePool(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

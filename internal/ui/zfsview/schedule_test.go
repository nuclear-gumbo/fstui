// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// scheduledSource is a Source that also knows scrub schedules.
type scheduledSource struct {
	*fakeSource
	plans map[string]zfs.ScrubSchedule
}

func (s *scheduledSource) ScrubSchedules(context.Context, []zfs.Overview) (map[string]zfs.ScrubSchedule, error) {
	return s.plans, nil
}

// poolsWithSchedules loads the checksum-repaired pool, whose last scrub
// is overdue with a one-day threshold, and the schedules.
func poolsWithSchedules(t *testing.T, plans map[string]zfs.ScrubSchedule) *Pools {
	t.Helper()
	src := &scheduledSource{fakeSource: &fakeSource{pools: []zfs.Overview{golden(t, "checksum-repaired")}}, plans: plans}
	o := defaults()
	o.Thresholds.ScrubOverdueDays = 1
	p := NewPools(src, o)
	run(p, p.Init())
	return p
}

func TestScrubScheduled(t *testing.T) {
	p := poolsWithSchedules(t, map[string]zfs.ScrubSchedule{
		"fstuitest_g_cksum": {Next: now.Add(7 * 24 * time.Hour), By: "systemd timer zfs-scrub-weekly@fstuitest_g_cksum.timer"},
	})
	pressKey(p, "enter")
	out := render(p, 110, 30) + texts(p.visibleWarnings())
	for _, want := range []string{"next in 7d", "Scrubbed by systemd timer zfs-scrub-weekly@fstuitest_g_cksum.timer, next", "the next is in 7d"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestScrubNotScheduled(t *testing.T) {
	p := poolsWithSchedules(t, map[string]zfs.ScrubSchedule{})
	pressKey(p, "enter")
	out := render(p, 110, 30) + texts(p.visibleWarnings())
	for _, want := range []string{"no scrub scheduled", "No scrub is scheduled", "; no scrub is scheduled"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

// A source that cannot tell shows nothing about schedules, not "none".
func TestScrubScheduleUnknown(t *testing.T) {
	p := NewPools(&fakeSource{pools: []zfs.Overview{golden(t, "checksum-repaired")}}, defaults())
	run(p, p.Init())
	pressKey(p, "enter")
	if out := render(p, 110, 30); strings.Contains(out, "schedul") {
		t.Errorf("schedule shown without a schedule source:\n%s", out)
	}
}

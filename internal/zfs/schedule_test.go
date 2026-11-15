// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testdataFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The capture had a weekly timer for fstuitest_sched and a monthly one
// for fstuitest_a-b, whose name systemd escapes as fstuitest_a\x2db.
func TestParseScrubTimers(t *testing.T) {
	got, err := ParseScrubTimers(testdataFile(t, "systemd", "257", "list-timers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	w, m := got["fstuitest_sched"], got["fstuitest_a-b"]
	if !w.Next.Equal(time.UnixMicro(1794811357076404)) || w.By != "systemd timer zfs-scrub-weekly@fstuitest_sched.timer" {
		t.Errorf("weekly %+v", w)
	}
	if !m.Next.Equal(time.UnixMicro(1796106094221020)) {
		t.Errorf("monthly %+v", m)
	}
}

func TestUnescapeUnit(t *testing.T) {
	if got, err := unescapeUnit(`tank\x2dold\x2e1`); err != nil || got != "tank-old.1" {
		t.Errorf("%q, %v", got, err)
	}
	for _, bad := range []string{`tank\x2`, `tank\y20`, `tank\`} {
		if _, err := unescapeUnit(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDebianCron(t *testing.T) {
	runs, shipped := DebianCronSchedule(testdataFile(t, "debian", "13", "cron.d-zfsutils-linux"))
	if !runs || !shipped {
		t.Errorf("shipped file: runs %v, shipped %v", runs, shipped)
	}
	if runs, shipped := DebianCronSchedule([]byte("0 3 * * 0 root /usr/lib/zfs-linux/scrub\n")); !runs || shipped {
		t.Errorf("changed line: runs %v, shipped %v", runs, shipped)
	}
	if runs, _ := DebianCronSchedule([]byte("# 24 0 8-14 * * root /usr/lib/zfs-linux/scrub\n")); runs {
		t.Error("a commented line runs")
	}
}

// November 2026: the second Sunday is the 8th; December's is the 13th.
func TestNextDebianScrub(t *testing.T) {
	loc := time.UTC
	for _, c := range []struct{ after, want time.Time }{
		{time.Date(2026, 11, 1, 12, 0, 0, 0, loc), time.Date(2026, 11, 8, 0, 24, 0, 0, loc)},
		{time.Date(2026, 11, 8, 0, 24, 0, 0, loc), time.Date(2026, 12, 13, 0, 24, 0, 0, loc)},
		{time.Date(2026, 12, 20, 0, 0, 0, 0, loc), time.Date(2027, 1, 10, 0, 24, 0, 0, loc)},
	} {
		if got := NextDebianScrub(c.after); !got.Equal(c.want) {
			t.Errorf("after %v: %v, want %v", c.after, got, c.want)
		}
	}
}

func TestDebianScrubs(t *testing.T) {
	for _, c := range []struct {
		health, prop string
		want         bool
	}{{"ONLINE", "", true}, {"ONLINE", "-", true}, {"ONLINE", "enable", true}, {"ONLINE", "disable", false}, {"DEGRADED", "", false}} {
		if got := DebianScrubs(c.health, c.prop); got != c.want {
			t.Errorf("%s %q: %v", c.health, c.prop, got)
		}
	}
}

// A scrub timer of another shape, such as NixOS's zfs-scrub.timer, is
// reported as possible for every pool rather than ignored.
func TestOtherScrubTimer(t *testing.T) {
	got, err := ParseScrubTimers([]byte(`[{"next":1794811357076404,"unit":"zfs-scrub.timer"},{"next":0,"unit":"zfs-scrub-weekly@tank.timer"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if s := got[AnyPool]; s.By != "possibly systemd timer zfs-scrub.timer" || s.Next.IsZero() {
		t.Errorf("other timer %+v", s)
	}
	if s := got["tank"]; s.By != "systemd timer zfs-scrub-weekly@tank.timer" || !s.Next.IsZero() {
		t.Errorf("tank %+v", s)
	}
}

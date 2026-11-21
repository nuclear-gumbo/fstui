// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func goldenARC(t *testing.T, name string) zfs.ARCStats {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", "arcstats", name))
	if err != nil {
		t.Fatal(err)
	}
	s, err := zfs.ParseARCStats(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// arcAt feeds the view readings one second apart, as the poller would.
type arcFeed struct {
	t     *testing.T
	a     *ARC
	clock time.Time
	next  zfs.ARCStats
}

func newARCFeed(t *testing.T) *arcFeed {
	f := &arcFeed{t: t, clock: now}
	f.a = NewARC(func() (zfs.ARCStats, error) { return f.next, nil }, nil, func() time.Time { return f.clock })
	return f
}

func (f *arcFeed) read(s zfs.ARCStats, after time.Duration) {
	f.clock = f.clock.Add(after)
	f.next = s
	f.a.poll.inFlight = false
	cmd := f.a.poll.start()
	f.a.Update(cmd())
}

func TestARCHitRatioGraph(t *testing.T) {
	f := newARCFeed(t)
	base := goldenARC(t, "no-l2arc.txt")
	f.read(base, 0)
	if _, ok := f.a.hits.last(); ok {
		t.Fatal("one reading gave a rate")
	}
	next := base
	next.Hits += 90
	next.Misses += 10
	f.read(next, time.Second)
	if v, ok := f.a.hits.last(); !ok || math.Abs(v-90) > 1e-9 {
		t.Fatalf("hit ratio = %v, %v; want 90", v, ok)
	}
	out := render3(f.a, 30)
	for _, want := range []string{"now 90.0%", "since load", "100% ", "  0% ", "max  ", "size, last 5 minutes", "of 1.2 GiB maximum", "recently used", "compressed"} {
		if !strings.Contains(out, want) {
			t.Errorf("ARC view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "L2ARC") {
		t.Error("L2ARC shown without cache devices")
	}
	// The size graph is relative to the maximum: the capture's ARC holds 602800
	// bytes of 1293381120.
	if v, ok := f.a.sizes.last(); !ok || math.Abs(v-602800.0*100/1293381120) > 1e-9 {
		t.Errorf("size = %v%% of the maximum", v)
	}

	// No lookups in the next second: no new value, the last one stays.
	f.read(next, time.Second)
	if v, _ := f.a.hits.last(); v != 90 {
		t.Errorf("an idle second changed the ratio to %v", v)
	}
}

func TestARCL2(t *testing.T) {
	f := newARCFeed(t)
	s := goldenARC(t, "l2arc.txt")
	f.read(s, 0)
	s.L2Hits++
	s.L2Misses += 3
	f.read(s, time.Second)
	out := render3(f.a, 30)
	if !strings.Contains(out, "L2ARC") || !strings.Contains(out, "hit ratio now 25.0%") || !strings.Contains(out, "198 MiB on cache devices") {
		t.Errorf("L2ARC line:\n%s", out)
	}
	// The cache pool is gone: the counters remain, the line does not.
	s.L2Devices = 0
	f.read(s, time.Second)
	if out := render3(f.a, 30); strings.Contains(out, "L2ARC") {
		t.Errorf("L2ARC shown with no cache devices:\n%s", out)
	}
}

// Shown, the ARC is read every second; hidden, every ten.
func TestARCRates(t *testing.T) {
	f := newARCFeed(t)
	if f.a.poll.interval != config.Default().Refresh.ARCHidden {
		t.Fatalf("starts at %v", f.a.poll.interval)
	}
	if cmd := f.a.Activate(); cmd == nil || f.a.poll.interval != config.Default().Refresh.ARC {
		t.Fatalf("activate: %v, cmd %v", f.a.poll.interval, cmd != nil)
	}
	f.a.Deactivate()
	if f.a.poll.interval != config.Default().Refresh.ARCHidden {
		t.Fatalf("deactivate: %v", f.a.poll.interval)
	}
	var _ ui.Deactivator = f.a
	var _ ui.Activator = f.a
}

// A ten-second background reading fills the ten one-second slots it
// covers; time no reading covers stays empty.
func TestSeriesSlots(t *testing.T) {
	var s series
	t0 := now
	s.add(t0, t0.Add(10*time.Second), 50)
	s.add(t0.Add(10*time.Second), t0.Add(11*time.Second), 80)
	got := s.slots(t0.Add(12*time.Second), 12*time.Second, 12)
	for i, v := range got {
		want := 50.0
		switch i {
		case 10:
			want = 80
		case 11:
			want = math.NaN()
		}
		if !(v == want || math.IsNaN(v) && math.IsNaN(want)) {
			t.Errorf("slot %d = %v, want %v", i, v, want)
		}
	}
	s.trim(t0.Add(10*time.Second + time.Millisecond))
	if len(s.points) != 1 {
		t.Errorf("trim kept %d points", len(s.points))
	}
}

func TestARCLoadingAndRefresh(t *testing.T) {
	f := newARCFeed(t)
	if out := render3(f.a, 30); !strings.Contains(out, "Reading ARC statistics") {
		t.Errorf("before the first reading: %q", out)
	}
	if _, cmd := f.a.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd == nil {
		t.Error("r did not read")
	}
}

func render3(a *ARC, h int) string { return ansi.Strip(a.Render(display(), 100, h)) }

func TestARCGraphRows(t *testing.T) {
	f := newARCFeed(t)
	f.read(goldenARC(t, "no-l2arc.txt"), 0)
	// The hit ratio graph, between its 100% and the 0% after it, takes
	// half the rows the rest leaves: never fewer than three, never more
	// than twelve.
	graphRows := func(h int) int {
		lines := strings.Split(render3(f.a, h), "\n")
		top, bottom := -1, -1
		for i, l := range lines {
			if strings.HasPrefix(l, "100% ") {
				top = i
			}
			if top >= 0 && bottom < 0 && strings.HasPrefix(l, "  0% ") {
				bottom = i
			}
		}
		return bottom - top + 1
	}
	if tiny, mid, tall := graphRows(6), graphRows(30), graphRows(40); tiny != arcGraphMin || mid != 10 || tall != arcGraphMax {
		t.Errorf("graph rows: %d at 6 lines, %d at 30, %d at 40", tiny, mid, tall)
	}
}

// refresh.arc sets the interval while the view is shown and
// refresh.arc_hidden while it is not, including after a saved change.
func TestARCIntervalsFollowSettings(t *testing.T) {
	set := config.Default()
	set.Refresh.ARC, set.Refresh.ARCHidden = 7*time.Second, 70*time.Second
	a := NewARC(func() (zfs.ARCStats, error) { return zfs.ARCStats{}, nil }, &set, time.Now)
	a.Activate()
	if a.poll.interval != 7*time.Second {
		t.Fatalf("shown interval %v", a.poll.interval)
	}
	set.Refresh.ARC = 3 * time.Second
	a.Update(ui.ConfigMsg{Config: set})
	if a.poll.interval != 3*time.Second {
		t.Errorf("after a saved change: %v", a.poll.interval)
	}
	a.Deactivate()
	if a.poll.interval != 70*time.Second {
		t.Errorf("hidden interval %v", a.poll.interval)
	}
}

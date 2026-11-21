// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// arcWindow is a window over which the ARC counters grew by delta, with
// size, c and c_max as given.
func arcWindow(t *testing.T, delta map[string]uint64, now map[string]uint64) *Window {
	t.Helper()
	k := func(vals map[string]uint64) zfs.Kstat {
		var b strings.Builder
		b.WriteString("1 1 0x01 1 1 1 1\nname type data\n")
		for n, v := range vals {
			fmt.Fprintf(&b, "%s 4 %d\n", n, v)
		}
		kst, err := zfs.ParseKstat([]byte(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		return kst
	}
	end := map[string]uint64{}
	for n, v := range delta {
		end[n] = v
	}
	for n, v := range now {
		end[n] = v
	}
	t0 := time.Unix(1_800_000_000, 0)
	var w Window
	w.Sample(zfs.TuneSample{At: t0, ARC: k(map[string]uint64{"hits": 0})})
	w.Sample(zfs.TuneSample{At: t0.Add(time.Minute), ARC: k(end)})
	return &w
}

func TestARC(t *testing.T) {
	full := map[string]uint64{"size": 8 << 30, "c": 8 << 30, "c_max": 8 << 30}
	cases := []struct {
		name    string
		delta   map[string]uint64
		now     map[string]uint64
		verdict Verdict
		want    string
	}{
		{"ghosts", map[string]uint64{"hits": 90000, "misses": 10000, "mru_ghost_hits": 4000}, full, WorthALook, "raise zfs_arc_max"},
		{"ghosts, small ARC", map[string]uint64{"hits": 90000, "misses": 10000, "mfu_ghost_hits": 4000}, map[string]uint64{"size": 1 << 30, "c_max": 8 << 30}, WorthALook, "under half its maximum"},
		{"prefetch", map[string]uint64{"hits": 90000, "misses": 10000, "prefetch_data_misses": 5000, "demand_hit_predictive_prefetch": 200}, full, WorthALook, "zfs_prefetch_disable"},
		{"metadata", map[string]uint64{"hits": 90000, "misses": 10000, "demand_metadata_hits": 18000, "demand_metadata_misses": 4000}, full, WorthALook, "zfs_arc_meta_balance"},
		{"l2arc", map[string]uint64{"hits": 90000, "misses": 10000, "l2_hits": 50, "l2_misses": 9000}, map[string]uint64{"l2_hdr_size": 200 << 20, "c_max": 8 << 30}, WorthALook, "smaller cache device"},
		{"fits", map[string]uint64{"hits": 99000, "misses": 1000}, full, Fits, "would not change much"},
		{"too few", map[string]uint64{"hits": 900, "misses": 100}, full, NotEnough, "1000 lookups"},
	}
	for _, c := range cases {
		f := ARC(arcWindow(t, c.delta, c.now), bytesOnly, dt)
		if f.Verdict != c.verdict || !strings.Contains(f.Figure+" "+f.Points, c.want) {
			t.Errorf("%s: %s, %q, %q", c.name, f.Verdict.Word(), f.Figure, f.Points)
		}
	}
}

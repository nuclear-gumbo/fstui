// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"math"
	"strings"
	"testing"
)

func TestParseARCStats(t *testing.T) {
	s, err := ParseARCStats(golden(t, "arcstats", "no-l2arc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Values from the capture (OpenZFS 2.3.9, a 2 GiB VM).
	if s.Size != 602800 || s.Target != 102286720 || s.Max != 1293381120 || s.Hits != 579481 || s.Misses != 372 || s.IOHits != 17 {
		t.Errorf("parsed %+v", s)
	}
	if s.L2Devices != 0 {
		t.Error("L2ARC reported on a system without cache devices")
	}

	l2, err := ParseARCStats(golden(t, "arcstats", "l2arc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if l2.L2Devices != 1 || l2.L2Size != 207292416 || l2.L2Hits != 144 || l2.L2Misses != 1678 {
		t.Errorf("L2ARC: %+v", l2)
	}
	if l2.Memory != 2069409792 || l2.MemoryFree != 621393536 {
		t.Errorf("memory: %d, %d", l2.Memory, l2.MemoryFree)
	}
}

func TestParseARCStatsErrors(t *testing.T) {
	head := "19 1 0x01 148 40256 1 2\nname type data\n"
	if _, err := ParseARCStats([]byte(head + "size 4 1\n")); err == nil || !strings.Contains(err.Error(), "c, c_max, c_min, hits, misses") {
		t.Errorf("missing fields: %v", err)
	}
	full := head + "size 4 1\nc 4 1\nc_min 4 1\nc_max 4 1\nhits 4 1\nmisses 4 x\n"
	if _, err := ParseARCStats([]byte(full)); err == nil || !strings.Contains(err.Error(), "misses") {
		t.Errorf("bad number: %v", err)
	}
	// A negative available memory is valid: the ARC is under pressure.
	neg := strings.Replace(full, "misses 4 x", "misses 4 1", 1) + "memory_available_bytes 3 -4096\n"
	if s, err := ParseARCStats([]byte(neg)); err != nil || s.MemoryFree != -4096 {
		t.Errorf("negative available memory: %+v, %v", s, err)
	}
}

func TestHitRatio(t *testing.T) {
	a := ARCStats{Hits: 100, IOHits: 5, Misses: 10}
	b := ARCStats{Hits: 190, IOHits: 5, Misses: 20}
	if r, ok := HitRatio(a, b); !ok || math.Abs(r-0.9) > 1e-9 {
		t.Errorf("90 hits of 100 lookups = %v, %v", r, ok)
	}
	if _, ok := HitRatio(b, b); ok {
		t.Error("no lookups gave a ratio")
	}
	if _, ok := HitRatio(b, a); ok {
		t.Error("counters going backwards (module reloaded) gave a ratio")
	}
	if r, ok := L2HitRatio(ARCStats{L2Hits: 1, L2Misses: 1}, ARCStats{L2Hits: 2, L2Misses: 4}); !ok || math.Abs(r-0.25) > 1e-9 {
		t.Errorf("L2 = %v, %v", r, ok)
	}
}

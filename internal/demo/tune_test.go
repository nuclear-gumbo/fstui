// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Tune mode measures in the demo from replayed kstats: each sample names
// the pool's own datasets, and no counter it watches goes backwards, so
// its window grows as on a real pool.
func TestTuneReplay(t *testing.T) {
	c := New()
	a, err := NewARC()
	if err != nil {
		t.Fatal(err)
	}
	c.UseARC(a)
	r := zfs.NewReader(c)
	r.ReadFile, r.ReadDir = c.ReadFile, c.ReadDir
	for _, pool := range []string{"tank", "archive"} {
		replayGrows(t, r, pool)
	}
}

// replayGrows samples pool through every phase and checks no counter
// goes backwards.
func replayGrows(t *testing.T, r *zfs.Reader, pool string) {
	t.Helper()
	p, _ := name.ParsePool(pool)
	prev, err := r.ReadTuneSample(p, time.Now())
	if err != nil || len(prev.Objsets) == 0 || len(prev.Txgs) == 0 {
		t.Fatalf("%s: %d objsets, %d txgs, %v", pool, len(prev.Objsets), len(prev.Txgs), err)
	}
	for ds := range prev.Objsets {
		if ds != pool && !strings.HasPrefix(ds, pool+"/") {
			t.Errorf("%s: dataset %s is not the pool's", pool, ds)
		}
	}
	for range tunePhases {
		cur, err := r.ReadTuneSample(p, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for ds, k := range cur.Objsets {
			if counter(prev.Objsets[ds], "nwritten") > counter(k, "nwritten") {
				t.Errorf("%s: %s nwritten went back", pool, ds)
			}
		}
		if counter(prev.ARC, "hits") > counter(cur.ARC, "hits") {
			t.Error("ARC hits went back")
		}
		prev = cur
	}
}

func counter(k zfs.Kstat, field string) uint64 {
	n, _ := k.Uint(field)
	return n
}

// SPDX-License-Identifier: GPL-3.0-or-later

// Package tune answers whether a pool's configuration fits its workload
// (tune mode), from what was measured over a window of time: never from
// counters since boot, so the answer describes the current workload.
package tune

import (
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// Window is what was measured since measuring started or was reset: the
// kstats at its start and now, and the zpool iostat intervals summed.
type Window struct {
	Start, Now zfs.TuneSample
	// Seconds of zpool iostat intervals summed into IO.
	Seconds float64
	IO      map[string]*VdevTotals // by vdev name as zpool iostat prints it
	// Restarted is set when a counter went backwards (the pool was
	// exported and imported, or the module reloaded) and the window
	// started again.
	Restarted bool
}

// VdevTotals is one vdev's I/O over the window.
type VdevTotals struct {
	ReadBytes, WriteBytes float64
	ReadOps, WriteOps     float64
	// Wait sums are latency × operations, ns: divided by the operations
	// they give the window's average.
	TotalWait, DiskWait [2]float64 // read, write
}

// Sample records a kstat reading: the first one starts the window, and
// one whose counters went backwards starts it again.
func (w *Window) Sample(s zfs.TuneSample) {
	if w.Start.At.IsZero() || wentBack(w.Now, s) {
		restarted := !w.Start.At.IsZero()
		*w = Window{Start: s, Restarted: restarted}
	}
	w.Now = s
}

// wentBack reports whether a counter that only grows is smaller in cur
// than in prev.
func wentBack(prev, cur zfs.TuneSample) bool {
	for ds, k := range cur.Objsets {
		if p, ok := prev.Objsets[ds]; ok && counter(k, "nwritten") < counter(p, "nwritten") {
			return true
		}
	}
	return counter(cur.ARC, "hits") < counter(prev.ARC, "hits")
}

func counter(k zfs.Kstat, name string) uint64 {
	n, _ := k.Uint(name)
	return n
}

// AddIO adds one zpool iostat -l interval of secs seconds: the pool's row
// and its devices'.
func (w *Window) AddIO(p zfs.IOPool, secs float64) {
	if w.IO == nil {
		w.IO = map[string]*VdevTotals{}
	}
	w.Seconds += secs
	for _, v := range append([]zfs.IOStat{p.IOStat}, p.Devices...) {
		t := w.IO[v.Name.Raw()]
		if t == nil {
			t = &VdevTotals{}
			w.IO[v.Name.Raw()] = t
		}
		rops, wops := float64(v.ReadOps)*secs, float64(v.WriteOps)*secs
		t.ReadOps += rops
		t.WriteOps += wops
		t.ReadBytes += float64(v.ReadBytes) * secs
		t.WriteBytes += float64(v.WriteBytes) * secs
		t.TotalWait[0] += float64(v.ReadWait) * rops
		t.TotalWait[1] += float64(v.WriteWait) * wops
		t.DiskWait[0] += float64(v.ReadDiskWait) * rops
		t.DiskWait[1] += float64(v.WriteDiskWait) * wops
	}
}

// Objset is how much a dataset's counter grew over the window; ok is
// false when the dataset was not there at both ends.
func (w *Window) Objset(ds, field string) (n uint64, ok bool) {
	s, ok1 := w.Start.Objsets[ds]
	e, ok2 := w.Now.Objsets[ds]
	if !ok1 || !ok2 {
		return 0, false
	}
	a, ok1 := s.Uint(field)
	b, ok2 := e.Uint(field)
	if !ok1 || !ok2 || b < a {
		return 0, false
	}
	return b - a, true
}

// Global is how much a global counter grew (in arcstats, dmu_tx, zil or
// zfetchstats, chosen by pick).
func (w *Window) Global(pick func(zfs.TuneSample) zfs.Kstat, field string) uint64 {
	a, b := counter(pick(w.Start), field), counter(pick(w.Now), field)
	if b < a {
		return 0
	}
	return b - a
}

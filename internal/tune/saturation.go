// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"fmt"
	"slices"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// SaturationQuestion is the question Saturation answers.
const SaturationQuestion = "What limits the pool"

// Thresholds of the saturation question: at least minWindow seconds and
// minOpsSeen operations; a device is slow at slowFactor times its
// siblings' median disk wait; ZFS queues matter when the total wait is
// queueFactor times the disk wait; transaction groups are slow past
// txgTimeout, zfs_txg_timeout's default.
const (
	minOpsSeen  = 1000
	slowFactor  = 2.0
	queueFactor = 4.0
	txgTimeout  = 5 * time.Second
	minLatency  = float64(time.Millisecond) // waits below this are never a limit
)

// Saturation answers what limits the pool, if anything: the write
// throttle, transaction groups that sync too slowly, memory, a slow
// device, ZFS's own queues, or ZIL stalls.
func Saturation(w *Window, l Layout) Finding {
	f := Finding{Question: SaturationQuestion}
	var ops float64
	for _, leaf := range l.Leaves {
		if t := w.IO[leaf]; t != nil {
			ops += t.ReadOps + t.WriteOps
		}
	}
	if w.Seconds < minWindow || ops < minOpsSeen {
		f.Figure = fmt.Sprintf("the pool's devices did %.0f operations in %.0f s of measuring; it takes %d over a minute", ops, w.Seconds, minOpsSeen)
		f.Points = "leave the tab open while the pool is busy"
		return f
	}
	g := func(pick func(zfs.TuneSample) zfs.Kstat, n string) uint64 { return w.Global(pick, n) }
	dmu := func(s zfs.TuneSample) zfs.Kstat { return s.DMUTx }
	throttled := g(dmu, "dmu_tx_dirty_throttle") + g(dmu, "dmu_tx_dirty_delay") + g(dmu, "dmu_tx_dirty_over_max")
	memory := g(func(s zfs.TuneSample) zfs.Kstat { return s.ARC }, "memory_throttle_count")
	stalls := g(func(s zfs.TuneSample) zfs.Kstat { return s.ZIL }, "zil_commit_stall_count")
	syncAvg, synced := txgSync(w)
	slow, slowWait, median := slowestLeaf(w, l.Leaves)
	queued, total, disk := queueing(w, l.Leaves)
	f.Evidence = []string{
		fmt.Sprintf("writes delayed by the write throttle: %d", throttled),
		fmt.Sprintf("transaction groups synced: %d, %s on average (zfs_txg_timeout is 5 s by default)", synced, syncAvg.Round(time.Millisecond)),
		fmt.Sprintf("ARC memory throttles: %d", memory),
		fmt.Sprintf("total wait %s against disk wait %s, all devices", time.Duration(total).Round(time.Microsecond), time.Duration(disk).Round(time.Microsecond)),
		fmt.Sprintf("ZIL commit stalls: %d", stalls),
	}
	if slow != "" {
		f.Evidence = append(f.Evidence, fmt.Sprintf("slowest device %s: disk wait %s, the others' median %s", slow, time.Duration(slowWait).Round(time.Microsecond), time.Duration(median).Round(time.Microsecond)))
	}
	switch {
	case throttled > 0:
		f.Verdict, f.Figure = Mismatch, fmt.Sprintf("the write throttle delayed writers %d times", throttled)
		f.Points = "dirty data reached its limit: the devices cannot write as fast as data arrives (faster devices or more vdevs; zfs_dirty_data_max only moves the point)"
	case synced > 0 && syncAvg > txgTimeout:
		f.Verdict, f.Figure = Mismatch, fmt.Sprintf("transaction groups take %s to sync, more than the %s between them", syncAvg.Round(time.Millisecond), txgTimeout)
		f.Points = "the pool cannot write a group before the next is due: faster devices or more vdevs"
	case memory > 0:
		f.Verdict, f.Figure = Mismatch, fmt.Sprintf("the ARC was throttled for memory %d times", memory)
		f.Points = "the system is short of memory: zfs_arc_max lower than now, or more RAM"
	case slow != "" && slowWait >= slowFactor*median && slowWait >= minLatency:
		f.Verdict, f.Figure = WorthALook, fmt.Sprintf("%s waits %.1f times as long as the other devices", slow, slowWait/max(median, 1))
		f.Points = "one slow device holds back its vdev: check its health (smartctl) and its cabling"
	case queued:
		f.Verdict, f.Figure = WorthALook, fmt.Sprintf("requests wait %s in total but %s at the disks", time.Duration(total).Round(time.Microsecond), time.Duration(disk).Round(time.Microsecond))
		f.Points = "requests queue in ZFS before the disks see them: the devices are busy; more vdevs spread the load"
	case stalls > 0:
		f.Verdict, f.Figure = WorthALook, fmt.Sprintf("ZIL commits stalled %d times", stalls)
		f.Points = "sync writes wait for each other: a log device on fast storage shortens them"
	default:
		f.Verdict, f.Figure = Fits, "no throttling, no slow device, and little queueing in ZFS"
		f.Points = "nothing measured limits the pool"
	}
	return f
}

// txgSync is the average sync time of the transaction groups committed
// in the window.
func txgSync(w *Window) (time.Duration, int) {
	var last uint64
	for _, x := range w.Start.Txgs {
		last = max(last, x.TXG)
	}
	var sum time.Duration
	n := 0
	for _, x := range w.Now.Txgs {
		if x.TXG > last && x.State == "C" {
			sum += time.Duration(min(x.SyncTime, uint64(time.Hour))) // a sync time beyond an hour is not a real one
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / time.Duration(n), n
}

// slowestLeaf is the leaf with the longest average disk wait, its wait
// and the median of the others (ns).
func slowestLeaf(w *Window, leaves []string) (name string, wait, median float64) {
	waits := map[string]float64{}
	for _, leaf := range leaves {
		t := w.IO[leaf]
		if t == nil || t.ReadOps+t.WriteOps < minOps {
			continue
		}
		waits[leaf] = (t.DiskWait[0] + t.DiskWait[1]) / (t.ReadOps + t.WriteOps)
	}
	if len(waits) < 2 {
		return "", 0, 0
	}
	var others []float64
	for _, leaf := range leaves { // in the pool's order, so ties settle the same way
		if v, ok := waits[leaf]; ok && v > wait {
			name, wait = leaf, v
		}
	}
	for leaf, v := range waits {
		if leaf != name {
			others = append(others, v)
		}
	}
	slices.Sort(others)
	return name, wait, others[len(others)/2]
}

// queueing reports whether requests wait in ZFS much longer than at the
// disks, with the average total and disk wait (ns) over all leaves.
func queueing(w *Window, leaves []string) (queued bool, total, disk float64) {
	var ops float64
	for _, leaf := range leaves {
		if t := w.IO[leaf]; t != nil {
			total += t.TotalWait[0] + t.TotalWait[1]
			disk += t.DiskWait[0] + t.DiskWait[1]
			ops += t.ReadOps + t.WriteOps
		}
	}
	if ops == 0 {
		return false, 0, 0
	}
	total, disk = total/ops, disk/ops
	return total >= queueFactor*disk && total >= minLatency, total, disk
}

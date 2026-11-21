// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"github.com/nuclear-gumbo/fstui/internal/config"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// AmplificationQuestion is the question WriteAmplification answers.
const AmplificationQuestion = "Write amplification"

// Layout is what a pool's data layout costs for each byte written, and
// which devices hold data or the ZIL (cache devices write L2ARC copies,
// not amplification).
type Layout struct {
	Factor float64 // bytes on the disks per byte of data, without compression
	Desc   string  // "a 3-wide raidz1", "a 2-way mirror"
	Raidz  bool
	Leaves []string // data, special, dedup and log leaves, as zpool iostat names them
}

// LayoutOf reads a pool's layout from its status. With top-level vdevs of
// different kinds, the factor is the largest, which ZFS writes to most.
func LayoutOf(st zfs.PoolStatus) Layout {
	l := Layout{Factor: 1, Desc: "a stripe"}
	for _, v := range st.Data.Children {
		f, desc := 1.0, "a stripe"
		w := float64(len(v.Children))
		switch v.Type {
		case "mirror":
			f, desc = w, fmt.Sprintf("a %d-way mirror", len(v.Children))
		case "raidz", "draid":
			p := float64(parity(v.Name.Raw()))
			if w > p {
				f, desc = w/(w-p), fmt.Sprintf("a %d-wide %s", len(v.Children), strings.SplitN(v.Name.Raw(), "-", 2)[0])
				l.Raidz = true
			}
		}
		if f > l.Factor || len(st.Data.Children) == 1 {
			l.Factor, l.Desc = f, desc
		}
	}
	st.Walk(func(v zfs.Vdev) {
		if len(v.Children) == 0 && v.Type != "root" && !slices.ContainsFunc(st.Cache, func(c zfs.Vdev) bool { return c.Name.Raw() == v.Name.Raw() }) &&
			!slices.ContainsFunc(st.Spares, func(c zfs.Vdev) bool { return c.Name.Raw() == v.Name.Raw() }) {
			l.Leaves = append(l.Leaves, v.Name.Raw())
		}
	})
	return l
}

// parity is the parity of a raidz or draid vdev from its name
// ("raidz2-0", "draid1:4d:6c:0s-0"); 1 when the name does not say.
func parity(name string) int {
	n := strings.TrimPrefix(strings.TrimPrefix(name, "raidz"), "draid")
	if n != "" && n[0] >= '1' && n[0] <= '3' {
		return int(n[0] - '0')
	}
	return 1
}

// WriteAmplification answers whether the disks write more than the
// layout explains for what applications wrote. compress is the pool's
// compression ratio (1 without compression).
func WriteAmplification(w *Window, l Layout, compress float64, size func(uint64) string, t config.Tune) Finding {
	minWritten := uint64(t.MinWrittenMiB) << 20 //nolint:gosec // a positive setting
	f := Finding{Question: AmplificationQuestion}
	var logical, logicalRead, zilNormal, zilSlog uint64
	for ds := range w.Now.Objsets {
		logical += objset(w, ds, "nwritten")
		logicalRead += objset(w, ds, "nread")
		zilNormal += objset(w, ds, "zil_itx_metaslab_normal_bytes")
		zilSlog += objset(w, ds, "zil_itx_metaslab_slog_bytes")
	}
	var physical, diskRead float64
	for _, leaf := range l.Leaves {
		if t := w.IO[leaf]; t != nil {
			physical += t.WriteBytes
			diskRead += t.ReadBytes
		}
	}
	if w.Seconds < t.MinWindow.Seconds() || logical < minWritten {
		f.Figure = fmt.Sprintf("applications have written %s in %.0f s; an answer needs %s written over %s", size(logical), w.Seconds, size(minWritten), format.Age(t.MinWindow))
		f.Points = "leave the tab open while the pool is written to"
		return f
	}
	expected := l.Factor / max(compress, 1)
	ratio := physical / float64(logical)
	f.Figure = fmt.Sprintf("the disks wrote %.1f times what applications wrote (%s for %s); %s with compression %.2fx accounts for %.1f", ratio,
		size(uint64(physical)), size(logical), l.Desc, max(compress, 1), expected)
	f.Details = []string{
		"written by applications: " + size(logical),
		"written to the disks: " + size(uint64(physical)),
		fmt.Sprintf("expected from the layout: %.2f (%s), divided by the compression ratio %.2f", l.Factor, l.Desc, max(compress, 1)),
		"sync writes through the ZIL on the pool's disks: " + size(zilNormal) + "; on log devices: " + size(zilSlog),
		"read from the disks: " + size(uint64(diskRead)) + "; read by applications: " + size(logicalRead),
	}
	switch {
	case ratio <= expected*t.AmplificationFits:
		f.Verdict, f.Points = Fits, "the layout explains what the disks write"
		return f
	case ratio <= expected*t.AmplificationMismatch:
		f.Verdict = WorthALook
	default:
		f.Verdict = Mismatch
	}
	f.Points = amplificationCause(l, logical, logicalRead, zilNormal, zilSlog, diskRead)
	return f
}

// amplificationCause names the largest part of the excess.
func amplificationCause(l Layout, logical, logicalRead, zilNormal, zilSlog uint64, diskRead float64) string {
	extraRead := diskRead - float64(logicalRead)
	switch {
	case zilNormal >= logical/4:
		return "sync writes are written twice, first to the ZIL on the pool's disks; a log device takes that write (sync=disabled drops it, losing the last seconds on a crash)"
	case zilSlog >= logical/4:
		return "sync writes also go to the log devices: the cost of sync writes, not of the layout"
	case extraRead > float64(logical)/4:
		return "small writes into larger records: each record is read, changed and rewritten whole; a recordsize near the write size avoids it"
	case l.Raidz:
		return "raidz pads small blocks and gives each its own parity; larger records, or a mirror for small-block data, cost less"
	}
	return "metadata and small writes into large records cost more than the data; see record size against request size"
}

// objset is a dataset's counter growth, 0 when it cannot be told.
func objset(w *Window, ds, field string) uint64 {
	n, _ := w.Objset(ds, field)
	return n
}

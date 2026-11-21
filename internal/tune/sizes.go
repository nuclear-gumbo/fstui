// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// bigRecord is the largest record the default limits allow without
// changing a module parameter; larger requests than records under it are
// worth a larger recordsize.
const bigRecord = 1 << 20

// SizesQuestion is the question Sizes answers.
const SizesQuestion = "Record size against request size"

// DatasetIO is a dataset's reads or writes over the window.
type DatasetIO struct {
	Dataset string
	Write   bool
	Bytes   uint64
	Ops     uint64
	Record  uint64 // recordsize, or volblocksize for a volume
}

// Avg is the average request size.
func (d DatasetIO) Avg() uint64 { return d.Bytes / max(d.Ops, 1) }

func (d DatasetIO) verb() string {
	if d.Write {
		return "writes"
	}
	return "reads"
}

// Cost says whether the disks did measurably more than applications
// asked: WriteAmplification found more than the layout explains, and
// ReadAmplified. Small requests are harmless when they are sequential, and
// averages cannot tell; the cost can.
type Cost struct{ Writes, Reads bool }

// Sizes answers whether the datasets' record sizes fit the size of the
// requests made to them. records maps a dataset to its recordsize (or
// volblocksize) in bytes; cost is what the disks did beyond the requests;
// size formats a byte count as the view does.
func Sizes(w *Window, records map[string]uint64, volumes map[string]bool, cost Cost, size func(uint64) string, t config.Tune) Finding {
	f := Finding{Question: SizesQuestion}
	io := datasetIO(w, records, t)
	if len(io) == 0 {
		f.Figure = fmt.Sprintf("no dataset has read or written %s in %d requests yet; an answer needs that much", size(uint64(t.MinMovedMiB)<<20), t.MinRequests) //nolint:gosec // a positive setting
		f.Points = "leave the tab open while the pool is in use"
		return f
	}
	f.Verdict = Fits
	f.Figure = describe(io[0], size)
	f.Points = "the record sizes suit the requests; small sequential requests cost nothing extra"
	for _, d := range io {
		f.Details = append(f.Details, describe(d, size))
		if f.Verdict == Fits {
			judgeSize(&f, d, cost, size, t)
			if f.Verdict != Fits && !volumes[d.Dataset] {
				f.Change = &Change{Kind: "property", Dataset: d.Dataset, Name: "recordsize", Value: sizeValue(recordFor(d.Avg()))}
			}
		}
	}
	return f
}

// datasetIO lists the reads and writes of each dataset with a known
// record size that moved enough to judge, the most bytes first.
func datasetIO(w *Window, records map[string]uint64, t config.Tune) []DatasetIO {
	minBytes, minOps := uint64(t.MinMovedMiB)<<20, uint64(t.MinRequests) //nolint:gosec // positive settings
	var io []DatasetIO
	for ds := range w.Now.Objsets {
		rec := records[ds]
		if rec == 0 {
			continue
		}
		for _, write := range []bool{false, true} {
			bytesField, opsField := "nread", "reads"
			if write {
				bytesField, opsField = "nwritten", "writes"
			}
			b, ok1 := w.Objset(ds, bytesField)
			n, ok2 := w.Objset(ds, opsField)
			if ok1 && ok2 && b >= minBytes && n >= minOps {
				io = append(io, DatasetIO{Dataset: ds, Write: write, Bytes: b, Ops: n, Record: rec})
			}
		}
	}
	slices.SortFunc(io, func(a, b DatasetIO) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), cmp.Compare(a.Dataset, b.Dataset), cmp.Compare(btoi(a.Write), btoi(b.Write)))
	})
	return io
}

// judgeSize sets f to "worth a look" when d's requests are much smaller
// than its records and the disks did more than asked, or much larger.
func judgeSize(f *Finding, d DatasetIO, cost Cost, size func(uint64) string, t config.Tune) {
	smallRatio, largeRatio := uint64(t.SmallRatio), uint64(t.LargeRatio) //nolint:gosec // positive settings
	switch {
	case d.Record >= smallRatio*d.Avg() && d.Write && cost.Writes:
		f.Verdict, f.Figure = WorthALook, describe(d, size)
		f.Points = "small writes into larger records rewrite whole records; a smaller recordsize on " + d.Dataset + " fits them, for data written from then on"
	case d.Record >= smallRatio*d.Avg() && !d.Write && cost.Reads:
		f.Verdict, f.Figure = WorthALook, describe(d, size)
		f.Points = "each small read fetches a whole record; a smaller recordsize on " + d.Dataset + " fits them, for data written from then on"
	case d.Avg() >= largeRatio*d.Record && d.Record < bigRecord:
		f.Verdict, f.Figure = WorthALook, describe(d, size)
		f.Points = "a larger recordsize on " + d.Dataset + " stores the same data in fewer blocks, for data written from then on"
	}
}

// describe is one dataset's line: "fstuitest_tune/small: writes average
// 4.0 KiB into 128 KiB records (7.8 MiB in 2000)".
func describe(d DatasetIO, size func(uint64) string) string {
	return fmt.Sprintf("%s: %s average %s into %s records (%s in %s)", d.Dataset, d.verb(),
		size(d.Avg()), size(d.Record), size(d.Bytes), strconv.FormatUint(d.Ops, 10))
}

// ReadAmplified reports whether the pool's devices read more than
// twice what applications read in the window, with at least minBytes
// read: reads fetch more than they need.
func ReadAmplified(w *Window, l Layout, t config.Tune) bool {
	minBytes := uint64(t.MinMovedMiB) << 20 //nolint:gosec // a positive setting
	var asked uint64
	for ds := range w.Now.Objsets {
		asked += objset(w, ds, "nread")
	}
	var disk float64
	for _, leaf := range l.Leaves {
		if t := w.IO[leaf]; t != nil {
			disk += t.ReadBytes
		}
	}
	return asked >= minBytes && disk > 2*float64(asked)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

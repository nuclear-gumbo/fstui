// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

var records = map[string]uint64{
	"fstuitest_tune": 128 << 10, "fstuitest_tune/seq": 1 << 20, "fstuitest_tune/small": 128 << 10,
	"fstuitest_tune/sync": 128 << 10, "fstuitest_tune/text": 128 << 10,
}

func bytesOnly(n uint64) string { return strconv.FormatUint(n, 10) + " B" }

func window(t *testing.T, from, to string) *Window {
	t.Helper()
	t0 := time.Unix(1_800_000_000, 0)
	var w Window
	w.Sample(phase(t, from, t0))
	w.Sample(phase(t, to, t0.Add(time.Minute)))
	return &w
}

func TestSizes(t *testing.T) {
	cases := []struct {
		from, to string
		verdict  Verdict
		figure   string
	}{
		{"0-base", "1-seqwrite", Fits, "fstuitest_tune/seq: writes average 1048576 B into 1048576 B records"},
		{"1-seqwrite", "2-smallwrite", WorthALook, "fstuitest_tune/small: writes average 4096 B into 131072 B records"},
		{"5-cold", "6-reads", WorthALook, "fstuitest_tune/seq: reads average 4096 B into 1048576 B records"},
		{"6-reads", "6-reads", NotEnough, "no dataset has read or written"},
	}
	for _, c := range cases {
		f := Sizes(window(t, c.from, c.to), records, nil, Cost{Writes: true, Reads: true}, bytesOnly)
		if f.Verdict != c.verdict || !strings.HasPrefix(f.Figure, c.figure) || f.Points == "" {
			t.Errorf("%s to %s: %s, %q, %q", c.from, c.to, f.Verdict.Word(), f.Figure, f.Points)
		}
	}
}

// Requests much larger than small records point at a larger recordsize.
func TestSizesLargeRequests(t *testing.T) {
	w := window(t, "0-base", "1-seqwrite")
	f := Sizes(w, map[string]uint64{"fstuitest_tune/seq": 128 << 10}, nil, Cost{}, bytesOnly)
	if f.Verdict != WorthALook || !strings.Contains(f.Points, "larger recordsize") {
		t.Errorf("%s, %q", f.Verdict.Word(), f.Points)
	}
}

// Small writes the disks did not pay extra for (sequential ones) fit.
func TestSizesSmallButSequential(t *testing.T) {
	f := Sizes(window(t, "3-syncwrite", "4-text"), records, nil, Cost{}, bytesOnly)
	if f.Verdict != Fits || !strings.Contains(f.Figure, "fstuitest_tune/text: writes average 8192 B") {
		t.Errorf("%s, %q", f.Verdict.Word(), f.Figure)
	}
}

// A finding about small writes points at a recordsize that fits them, on
// a filesystem; a volume's block size cannot change.
func TestSizesChange(t *testing.T) {
	w := window(t, "1-seqwrite", "2-smallwrite")
	f := Sizes(w, records, nil, Cost{Writes: true}, bytesOnly)
	if c := f.Change; c == nil || *c != (Change{Kind: "property", Dataset: "fstuitest_tune/small", Name: "recordsize", Value: "4K"}) {
		t.Errorf("change %+v", f.Change)
	}
	if f := Sizes(w, records, map[string]bool{"fstuitest_tune/small": true}, Cost{Writes: true}, bytesOnly); f.Change != nil {
		t.Errorf("a volume: change %+v", f.Change)
	}
	for avg, want := range map[uint64]string{1: "4K", 6000: "8K", 100 << 10: "128K", 3 << 20: "1M"} {
		if got := sizeValue(recordFor(avg)); got != want {
			t.Errorf("record for %d: %s, want %s", avg, got, want)
		}
	}
}

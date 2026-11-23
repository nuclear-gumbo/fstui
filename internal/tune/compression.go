// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// CompressionQuestion is the question Compression answers.
const CompressionQuestion = "How well data compresses"

// DatasetCompression is a dataset's compression as zfs get reports it.
type DatasetCompression struct {
	Dataset     string
	Algorithm   string  // the compression property
	Ratio       float64 // compressratio
	Used        uint64
	LogicalUsed uint64
}

// expensive algorithms cost CPU on every write; lz4 (and zle) give up
// early on data that does not compress.
func expensive(alg string) bool {
	switch alg {
	case "off", "lz4", "zle", "on", "":
		return false
	}
	return true
}

// Compression answers whether compression pays for itself per dataset;
// walk is the result of the per-file walk, if one was run.
// files is whether the pool's files can be read here: not for a pool on
// another host, whose files the per-file walk cannot reach.
func Compression(ds []DatasetCompression, walk *WalkResult, size func(uint64) string, t config.Tune, files bool) Finding {
	minUsed := uint64(t.CompressionGiB) << 30 //nolint:gosec // a non-negative setting
	f := Finding{Question: CompressionQuestion}
	var big []DatasetCompression
	for _, d := range ds {
		if d.Used >= minUsed {
			big = append(big, d)
		}
	}
	if len(big) == 0 {
		f.Figure = "no dataset of the pool holds " + size(minUsed) + " or more"
		f.Points = "compression matters little for so little data"
		return f
	}
	slices.SortFunc(big, func(a, b DatasetCompression) int {
		return cmp.Or(cmp.Compare(b.Used, a.Used), cmp.Compare(a.Dataset, b.Dataset))
	})
	for _, d := range big {
		f.Details = append(f.Details, fmt.Sprintf("%s: %s, ratio %.2fx, %s stored for %s of data", d.Dataset, d.Algorithm, d.Ratio, size(d.Used), size(d.LogicalUsed)))
	}
	if walk != nil {
		f.Details = append(f.Details, walk.lines(size)...)
	}
	f.Verdict = Fits
	f.Figure = fmt.Sprintf("%s compresses %.2fx (compression=%s)", big[0].Dataset, big[0].Ratio, big[0].Algorithm)
	f.Points = "c measures, per file, how much of the data does not compress"
	if !files {
		f.Points = "measuring per file reads the files, so it needs fstui on the machine that holds the pool"
	}
	for _, d := range big {
		if expensive(d.Algorithm) && d.Ratio < t.PoorRatio {
			f.Verdict = WorthALook
			f.Figure = fmt.Sprintf("%s holds %s compressed with %s at %.2fx", d.Dataset, size(d.Used), d.Algorithm, d.Ratio)
			f.Points = d.Dataset + " barely compresses; lz4 gives nearly the same space for less CPU, on data written from then on"
			f.Change = &Change{Kind: "property", Dataset: d.Dataset, Name: "compression", Value: "lz4"}
			return f
		}
	}
	if walk != nil && walk.Bytes > 0 && walk.Share() >= 0.5 {
		f.Verdict = WorthALook
		f.Figure = fmt.Sprintf("%.0f%% of the data in the files read does not compress", 100*walk.Share())
		f.Points = "where it is kept on its own dataset, compression=off there saves a little CPU; lz4 costs little either way"
	}
	return f
}

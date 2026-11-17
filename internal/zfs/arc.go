// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// ARCStatsPath is the kernel's ARC statistics file. Anyone can read it,
// so the ARC view works without root.
const ARCStatsPath = "/proc/spl/kstat/zfs/arcstats"

// ARCStats is one reading of the ARC, ZFS's in-memory read cache, and of
// the L2ARC on cache devices. Sizes are bytes; the counters count since
// the module was loaded, so rates come from the difference between two
// readings.
type ARCStats struct {
	Size   uint64 // size: what the ARC holds now
	Target uint64 // c: the size the ARC is adjusting towards
	Min    uint64 // c_min
	Max    uint64 // c_max

	Data     uint64 // data_size: cached file contents
	Metadata uint64 // metadata_size
	MRU      uint64 // mru_size: blocks read once recently
	MFU      uint64 // mfu_size: blocks read more than once

	// Compressed and Uncompressed are the cached blocks as stored and as
	// they would be uncompressed.
	Compressed, Uncompressed uint64

	// Hits found the block cached; IOHits found it already being read;
	// Misses had to read it. Together they are every ARC lookup.
	Hits, IOHits, Misses uint64

	L2Size     uint64 // l2_size: data cached on L2ARC devices, uncompressed
	L2Hits     uint64
	L2Misses   uint64
	L2Devices  uint64 // l2_ndev: cache devices in imported pools
	Memory     uint64 // memory_all_bytes: memory the ARC can draw from
	MemoryFree int64  // memory_available_bytes: negative under pressure
}

// ReadARCStats reads ARCStatsPath.
func ReadARCStats() (ARCStats, error) {
	b, err := os.ReadFile(ARCStatsPath)
	if err != nil {
		return ARCStats{}, fmt.Errorf("reading ARC statistics: %w", err)
	}
	return ParseARCStats(b)
}

// ParseARCStats parses arcstats (ParseKstat). Types 3 and 4 are signed
// and unsigned 64-bit integers; nothing else is used.
func ParseARCStats(b []byte) (ARCStats, error) {
	k, err := ParseKstat(b)
	if err != nil {
		return ARCStats{}, fmt.Errorf("reading ARC statistics: %w", err)
	}
	vals := k.raw
	var s ARCStats
	required := map[string]*uint64{
		"size": &s.Size, "c": &s.Target, "c_min": &s.Min, "c_max": &s.Max,
		"hits": &s.Hits, "misses": &s.Misses,
	}
	optional := map[string]*uint64{
		"data_size": &s.Data, "metadata_size": &s.Metadata, "mru_size": &s.MRU, "mfu_size": &s.MFU,
		"compressed_size": &s.Compressed, "uncompressed_size": &s.Uncompressed, "iohits": &s.IOHits,
		"l2_ndev": &s.L2Devices, "l2_size": &s.L2Size, "l2_hits": &s.L2Hits, "l2_misses": &s.L2Misses, "memory_all_bytes": &s.Memory,
	}
	var missing []string
	for name, dst := range required {
		v, ok := vals[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if err := parseStat(name, v, dst); err != nil {
			return ARCStats{}, err
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return ARCStats{}, fmt.Errorf("ARC statistics lack %s", strings.Join(missing, ", "))
	}
	for name, dst := range optional {
		if v, ok := vals[name]; ok {
			if err := parseStat(name, v, dst); err != nil {
				return ARCStats{}, err
			}
		}
	}
	if v, ok := vals["memory_available_bytes"]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return ARCStats{}, fmt.Errorf("ARC statistic memory_available_bytes: %w", err)
		}
		s.MemoryFree = n
	}
	return s, nil
}

func parseStat(name, v string, dst *uint64) error {
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return fmt.Errorf("ARC statistic %s: %w", name, errors.Unwrap(err))
	}
	*dst = n
	return nil
}

// HitRatio is the share of lookups between two readings that found their
// block cached, as arcstat(1) computes hit%: hits over hits, I/O hits and
// misses. ok is false when there were no lookups, or the counters went
// backwards because the module was reloaded.
func HitRatio(prev, cur ARCStats) (ratio float64, ok bool) {
	return share(prev.Hits, cur.Hits, prev.IOHits+prev.Misses, cur.IOHits+cur.Misses)
}

// L2HitRatio is the share of L2ARC lookups between two readings that hit.
func L2HitRatio(prev, cur ARCStats) (ratio float64, ok bool) {
	return share(prev.L2Hits, cur.L2Hits, prev.L2Misses, cur.L2Misses)
}

func share(hit0, hit1, other0, other1 uint64) (float64, bool) {
	if hit1 < hit0 || other1 < other0 {
		return 0, false
	}
	h, total := hit1-hit0, hit1-hit0+other1-other0
	if total == 0 {
		return 0, false
	}
	return float64(h) / float64(total), true
}

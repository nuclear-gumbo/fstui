// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "time"

// Tune holds tune mode's thresholds: when a question has enough data to
// answer, and where its verdicts change (docs/configuration.md, Tune).
type Tune struct {
	MinWindow             time.Duration // streamed I/O needed for the amplification and saturation questions
	MinWrittenMiB         int           // written by applications, for amplification
	MinRequests           int           // per dataset, for the size question
	MinMovedMiB           int           // per dataset, for the size question
	MinLookups            int           // ARC lookups, for the memory question
	MinOperations         int           // device operations, for saturation
	MinPrefetch           int           // prefetched blocks before judging prefetch
	CompressionGiB        int           // datasets smaller than this are not judged for compression
	WalkFiles             int           // the per-file compression walk stops after this many files
	WalkTime              time.Duration // or after this long
	SlowTXG               time.Duration // a transaction group syncing longer is slow
	SmallRatio            int           // records this many times the average request are too large
	LargeRatio            int           // requests this many records long are too large for them
	L2HeadersMiB          int           // L2ARC headers worth weighing against its hits
	AmplificationFits     float64       // within this many times what the layout explains, writes fit
	AmplificationMismatch float64       // past this many times, a mismatch
	GhostShare            float64       // share of misses that were just evicted, pointing at a larger ARC
	PrefetchUse           float64       // share of prefetched blocks read, below which prefetch is wasted
	MetadataMiss          float64       // share of metadata lookups missing, pointing at metadata crowded out
	L2Share               float64       // share of L2ARC lookups hitting, below which it costs more than it gives
	SlowDevice            float64       // a device this many times slower than its siblings
	Queueing              float64       // total wait this many times the disk wait is queueing in ZFS
	PoorRatio             float64       // an expensive algorithm earning less than this is worth a look
	Incompressible        float64       // files saving less than this ratio do not compress
}

func defaultTune() Tune {
	return Tune{
		MinWindow: time.Minute, MinWrittenMiB: 16, MinRequests: 50, MinMovedMiB: 1, MinLookups: 10000, MinOperations: 1000,
		MinPrefetch: 1000, CompressionGiB: 1, WalkFiles: 200000, WalkTime: time.Minute, SlowTXG: 5 * time.Second,
		SmallRatio: 8, LargeRatio: 4, L2HeadersMiB: 64,
		AmplificationFits: 1.5, AmplificationMismatch: 3, GhostShare: 0.25, PrefetchUse: 0.25, MetadataMiss: 0.1,
		L2Share: 0.1, SlowDevice: 2, Queueing: 4, PoorRatio: 1.05, Incompressible: 1.1,
	}
}

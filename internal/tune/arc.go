// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"fmt"

	"github.com/nuclear-gumbo/fstui/internal/config"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// ARCQuestion is the question ARC answers.
const ARCQuestion = "Where memory would buy speed"

// ARC answers where memory would make reads faster. The ARC is shared by
// every pool, so the answer is the same on each.
func ARC(w *Window, size func(uint64) string, t config.Tune) Finding {
	minLookups, minPrefetch, l2Headers := uint64(t.MinLookups), uint64(t.MinPrefetch), uint64(t.L2HeadersMiB)<<20 //nolint:gosec // positive settings
	f := Finding{Question: ARCQuestion}
	arc := func(n string) uint64 { return w.Global(func(s zfs.TuneSample) zfs.Kstat { return s.ARC }, n) }
	now := func(n string) uint64 { v, _ := w.Now.ARC.Uint(n); return v }
	hits, misses := arc("hits")+arc("iohits"), arc("misses")
	if hits+misses < minLookups {
		f.Figure = fmt.Sprintf("the ARC has answered %d lookups; an answer needs %d", hits+misses, minLookups)
		f.Points = "leave the tab open while the system reads"
		return f
	}
	ghosts := arc("mru_ghost_hits") + arc("mfu_ghost_hits")
	mdHits, mdMisses := arc("demand_metadata_hits"), arc("demand_metadata_misses")
	pfMisses, pfUsed := arc("prefetch_data_misses"), arc("demand_hit_predictive_prefetch")
	l2Hits, l2Misses := arc("l2_hits"), arc("l2_misses")
	pct := func(a, b uint64) float64 { return 100 * float64(a) / float64(max(b, 1)) }
	f.Details = []string{
		"the ARC is shared by every pool, so this answer is the same on each",
		fmt.Sprintf("hit ratio %.1f%% (%d hits, %d misses)", pct(hits, hits+misses), hits, misses),
		fmt.Sprintf("ghost hits (misses a larger ARC would have served): %d, %.0f%% of misses", ghosts, pct(ghosts, misses)),
		fmt.Sprintf("size %s of a target %s, maximum %s", size(now("size")), size(now("c")), size(now("c_max"))),
		fmt.Sprintf("metadata misses: %.1f%% of %d metadata lookups", pct(mdMisses, mdHits+mdMisses), mdHits+mdMisses),
		fmt.Sprintf("prefetched blocks later read: %d of %d read from disk", pfUsed, pfMisses),
	}
	if l2Hits+l2Misses > 0 || now("l2_hdr_size") > 0 {
		f.Details = append(f.Details, fmt.Sprintf("L2ARC: %.0f%% of %d lookups hit; its headers hold %s of the ARC",
			pct(l2Hits, l2Hits+l2Misses), l2Hits+l2Misses, size(now("l2_hdr_size"))))
	}
	f.Verdict = WorthALook
	switch {
	case float64(ghosts) >= t.GhostShare*float64(misses):
		f.Figure = fmt.Sprintf("%.0f%% of ARC misses were blocks it had just evicted (ghost hits)", pct(ghosts, misses))
		f.Points = "a larger ARC would have served them: raise zfs_arc_max (now " + size(now("c_max")) + ") if the memory is free"
		f.Change = &Change{Kind: "arc"}
		if now("size") < now("c_max")/2 {
			f.Points = "the ARC is under half its maximum: other memory use keeps it small; more RAM, or less other use, would help"
			f.Change = nil
		}
	case pfMisses >= minPrefetch && float64(pfUsed) < t.PrefetchUse*float64(pfMisses):
		f.Figure = fmt.Sprintf("of %d blocks prefetched from disk, %d were then read (%.0f%%)", pfMisses, pfUsed, pct(pfUsed, pfMisses))
		f.Points = "prefetch reads data nobody uses; for random reads, zfs_prefetch_disable=1 stops it"
		f.Change = &Change{Kind: "param", Name: "zfs_prefetch_disable", Value: "1"}
	case mdHits+mdMisses >= minLookups && float64(mdMisses) >= t.MetadataMiss*float64(mdHits+mdMisses):
		f.Figure = fmt.Sprintf("%.1f%% of metadata lookups missed", pct(mdMisses, mdHits+mdMisses))
		f.Points = "data pushes metadata out of the ARC; a larger zfs_arc_meta_balance (default 500) keeps more metadata"
		f.Change = &Change{Kind: "param", Name: "zfs_arc_meta_balance", Value: "2000"}
	case now("l2_hdr_size") >= l2Headers && float64(l2Hits) < t.L2Share*float64(l2Hits+l2Misses):
		f.Figure = fmt.Sprintf("the L2ARC served %.0f%% of what reached it, and its headers hold %s of the ARC", pct(l2Hits, l2Hits+l2Misses), size(now("l2_hdr_size")))
		f.Points = "the L2ARC's headers cost more ARC than its hits save; a smaller cache device, or none"
	default:
		f.Verdict = Fits
		f.Figure = fmt.Sprintf("hit ratio %.1f%%; few misses were recently evicted blocks", pct(hits, hits+misses))
		f.Points = "more memory would not change much"
	}
	return f
}

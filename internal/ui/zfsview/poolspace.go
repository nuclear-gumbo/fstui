// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// poolSpace is the Space tab's poller type: the datasets of one pool,
// named so a read for a pool the user has left is dropped.
type poolSpace struct {
	pool string
	list []zfs.Dataset
}

func newSpacePoller(src Source, pool name.Pool, o PoolsOptions) poller[poolSpace] {
	p := pool.String()
	return poller[poolSpace]{
		read: func(ctx context.Context) (poolSpace, error) {
			l, err := src.PoolDatasets(ctx, pool)
			return poolSpace{pool: p, list: l}, err
		},
		interval: o.DatasetInterval, now: o.Now, slow: true,
		accept: func(s poolSpace) bool { return s.pool == p },
	}
}

// heldParts are what a dataset holds on its own, without its children,
// which get rows of their own.
var heldParts = []spacePart{spaceParts[0], spaceParts[1], spaceParts[3]}

// held is the space a dataset is responsible for by itself: its data, its
// snapshots and its unused refreservation.
func held(d zfs.Dataset) uint64 {
	var sum uint64
	for _, p := range heldParts {
		n, _ := d.Properties.Number(p.prop)
		sum += n
	}
	return sum
}

// spaceRanking answers "what is holding this pool's space": one gauge per
// dataset, largest first, scaled to the largest.
func spaceRanking(disp ui.Display, o zfs.Overview, list []zfs.Dataset, th thresholdsView, width int) []string {
	muted := disp.Palette.Style(theme.Muted)
	rows := append([]zfs.Dataset(nil), list...)
	sort.SliceStable(rows, func(a, b int) bool { return held(rows[a]) > held(rows[b]) })
	var largest uint64
	if len(rows) > 0 {
		largest = held(rows[0])
	}
	nameCol := len("DATASET")
	for _, d := range rows {
		nameCol = max(nameCol, len(d.Name))
	}
	nameCol = min(nameCol, max(width-ui.BarWidth-2*sizeWidth-3*gap, len("DATASET")))

	lines := []string{spaceHeader(disp, o, th), legend(disp)}
	lines = append(lines, muted.Render(pad("DATASET", nameCol)+sp+strings.Repeat(" ", ui.BarWidth)+sp+lpad("HELD", sizeWidth)+sp+lpad("WRITTEN", sizeWidth)))
	for _, d := range rows {
		w, _ := d.Properties.Number("written")
		lines = append(lines, pad(leftTrunc(d.Name, nameCol), nameCol)+sp+heldGauge(disp, d, largest)+sp+
			lpad(ui.Size(held(d), disp.Exact), sizeWidth)+sp+lpad(ui.Size(w, disp.Exact), sizeWidth))
	}
	return lines
}

// thresholdsView is what the header needs from the configured thresholds.
type thresholdsView struct{ warn, crit int }

func spaceHeader(disp ui.Display, o zfs.Overview, th thresholdsView) string {
	pct := capacityPct(o)
	level := ui.LevelOf(pct, th.warn, th.crit)
	word := ui.LevelWord(level)
	if word != "" {
		tok := map[ui.Level]theme.Token{ui.Warning: theme.Warn, ui.Critical: theme.Error}[level]
		word = sp + disp.Palette.Style(tok).Render(word)
	}
	freeing, _ := o.Pool.Properties.Number("freeing")
	return fmt.Sprintf("%s  %d%% full%s  %s of %s used  %s freeing", o.Pool.Name, pct, word,
		ui.Size(o.Used, disp.Exact), ui.Size(o.Used+o.Available, disp.Exact), ui.Size(freeing, disp.Exact))
}

func legend(disp ui.Display) string {
	var parts []string
	for _, p := range heldParts {
		g := p.glyph
		if disp.ASCII {
			g = p.ascii
		}
		parts = append(parts, disp.Palette.Style(p.tok).Render(g)+" "+p.label)
	}
	return disp.Palette.Style(theme.Muted).Render(strings.Join(parts, sp))
}

// heldGauge draws one dataset's held space, scaled so the largest row
// fills the gauge; each part has its own glyph, so the split reads
// without colour.
func heldGauge(disp ui.Display, d zfs.Dataset, largest uint64) string {
	g, cells := partsGauge(disp, d, heldParts, largest)
	return g + strings.Repeat(" ", ui.BarWidth-cells)
}

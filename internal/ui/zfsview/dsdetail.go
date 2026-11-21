// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Dataset detail tabs.
const (
	dsTabSpace = iota
	dsTabProperties
	dsTabSnapshots
	dsTabPermissions
	dsNumTabs
)

var dsTabNames = []string{"Capacity", "Properties", "Snapshots", "Permissions"}

// Results name their dataset, so a read started before the user moved to
// another dataset is dropped (poller.accept).
type (
	dsProps struct {
		ds string
		d  zfs.Dataset
	}
	dsSnaps struct {
		ds   string
		list []zfs.SnapshotRow
	}
	dsAllow struct {
		ds     string
		delegs []zfs.Delegation
	}
)

// Snapshot sort columns, cycled with s.
const (
	sortCreated = iota
	sortUsed
	sortReferenced
	numSorts
)

var sortNames = [numSorts]string{"created", "used", "referenced"}

type dsDetail struct {
	ds      name.Dataset
	typ     zfs.DatasetType
	tab     int
	scroll  int
	props   poller[dsProps]
	snaps   poller[dsSnaps]
	allow   poller[dsAllow]
	sortBy  int
	reverse bool
	started bool // its reads have been started
	order   derived[listKey, []zfs.SnapshotRow]
}

func newDSDetail(src DatasetSource, ds name.Dataset, typ zfs.DatasetType, o DatasetsOptions) *dsDetail {
	s := ds.String()
	return &dsDetail{
		ds: ds, typ: typ,
		props: poller[dsProps]{
			read: func(ctx context.Context) (dsProps, error) {
				d, err := src.DatasetProperties(ctx, ds)
				return dsProps{ds: s, d: d}, err
			},
			interval: o.Settings.Refresh.DatasetDetail, now: o.Now,
			accept: func(p dsProps) bool { return p.ds == s },
		},
		snaps: poller[dsSnaps]{
			read: func(ctx context.Context) (dsSnaps, error) {
				l, err := src.DatasetSnapshots(ctx, ds)
				return dsSnaps{ds: s, list: l}, err
			},
			interval: o.Settings.Refresh.DatasetDetail, now: o.Now, slow: true, limits: &o.Settings.Limits,
			accept: func(p dsSnaps) bool { return p.ds == s },
		},
		allow: poller[dsAllow]{
			read: func(ctx context.Context) (dsAllow, error) {
				l, err := src.Allow(ctx, ds)
				return dsAllow{ds: s, delegs: l}, err
			},
			interval: o.Settings.Refresh.DatasetDetail, now: o.Now,
			accept: func(p dsAllow) bool { return p.ds == s },
		},
	}
}

// init starts the detail's reads once. A detail created while drawing
// (the split layout's first frame) starts them on the next message.
func (d *dsDetail) init() tea.Cmd {
	if d.started {
		return nil
	}
	d.started = true
	return tea.Batch(d.props.start(), d.snaps.start(), d.allow.start())
}

func (d *dsDetail) handle(msg tea.Msg) (tea.Cmd, bool) {
	if cmd, updated := d.props.handle(msg); cmd != nil || updated {
		return cmd, true
	}
	if cmd, updated := d.snaps.handle(msg); cmd != nil || updated {
		return cmd, true
	}
	if cmd, updated := d.allow.handle(msg); cmd != nil || updated {
		return cmd, true
	}
	return nil, false
}

func (d *dsDetail) bindings() []key.Binding {
	bs := []key.Binding{keys.As(keys.Right, "next tab"), keys.As(keys.Left, "previous tab"), keys.As(keys.Down, "scroll"), keys.As(keys.Up, "scroll back"), keys.Get(keys.Refresh), keys.As(keys.Back, "back")}
	if d.tab == dsTabSnapshots {
		bs = append([]key.Binding{keys.Get(keys.Sort), keys.Get(keys.Reverse)}, bs...)
	}
	return bs
}

// key handles keys in the open dataset; refreshList re-reads the listing
// the Space tab is drawn from.
func (d *dsDetail) key(msg tea.KeyPressMsg, refreshList func() tea.Cmd) tea.Cmd {
	switch {
	case key.Matches(msg, keys.As(keys.Right, "next tab")):
		d.tab, d.scroll = (d.tab+1)%dsNumTabs, 0
	case key.Matches(msg, keys.As(keys.Left, "previous tab")):
		d.tab, d.scroll = (d.tab+dsNumTabs-1)%dsNumTabs, 0
	case key.Matches(msg, keys.As(keys.Down, "scroll")):
		d.scroll++
	case key.Matches(msg, keys.As(keys.Up, "scroll back")):
		d.scroll = max(d.scroll-1, 0)
	case key.Matches(msg, keys.Get(keys.Sort)) && d.tab == dsTabSnapshots:
		d.sortBy = (d.sortBy + 1) % numSorts
	case key.Matches(msg, keys.Get(keys.Reverse)) && d.tab == dsTabSnapshots:
		d.reverse = !d.reverse
	case key.Matches(msg, keys.Get(keys.Refresh)):
		return tea.Batch(refreshList(), d.props.refresh(), d.snaps.refresh())
	}
	return nil
}

// Render implements ui.Screen.
func (v *Datasets) Render(disp ui.Display, width, height int) string {
	split := width >= v.opts.Settings.UI.SplitFrom && len(v.poll.data) > 0
	if split && v.detail == nil {
		v.adopt() // Capacity draws from the listing; reads start on the next message
	}
	var out string
	out, v.geo = splitPanels(disp, width, height, split, v.focus && v.detail != nil, v.treePanel, v.detailPanel)
	return out
}

func (v *Datasets) treePanel(disp ui.Display, width, height int, focused bool) string {
	iw, ih := ui.Inner(width, height)
	return ui.Panel{Title: "datasets", Focused: focused}.Render(disp, v.renderList(disp, iw, ih), width, height)
}

func (v *Datasets) detailPanel(disp ui.Display, width, height int, focused bool) string {
	iw, ih := ui.Inner(width, height)
	content := v.renderDetail(disp, iw, ih-summaryLines)
	if d, ok := v.listed(v.detail.ds); ok {
		content = dsSummary(disp, d, iw) + "\n" + content
	}
	return v.detailFrame(focused).Render(disp, content, width, height)
}

// dsSummary keeps the tree's figures in view beside a narrowed tree:
// where the dataset is mounted, its space, compression and encryption.
func dsSummary(disp ui.Display, d zfs.Dataset, width int) string {
	muted := disp.Palette.Style(theme.Muted)
	size := func(prop string) string {
		n, _ := d.Properties.Number(prop)
		return format.Size(n, disp.Exact)
	}
	where := muted.Render("volume ") + zfs.ZvolDevice(d.Name)
	if d.Type == zfs.Filesystem {
		where = muted.Render("mounted at ") + d.Properties["mountpoint"].Text.Display()
	}
	enc := encryption(disp, d)
	if enc == "" {
		enc = "off"
	}
	second := muted.Render("used ") + size("used") + sp + muted.Render("available ") + size("available") + sp +
		muted.Render("referenced ") + size("referenced") + sp + muted.Render("ratio ") +
		d.Properties["compressratio"].Text.Display() + "x" + sp + muted.Render("encryption ") + enc
	return leftTrunc(where, width) + "\n" + second + "\n"
}

func (v *Datasets) detailFrame(focused bool) ui.Panel {
	return ui.Panel{Title: v.detail.ds.String(), Tabs: dsTabNames, Active: v.detail.tab, Focused: focused}
}

func (v *Datasets) renderDetail(disp ui.Display, width, height int) string {
	d := v.detail
	listed, ok := v.listed(d.ds)
	if !ok {
		return fmt.Sprintf("Dataset %s no longer exists.\n", d.ds) +
			disp.Palette.Style(theme.Muted).Render("esc returns to the dataset list.")
	}
	var lines []string
	switch d.tab {
	case dsTabSpace:
		lines = spaceLines(disp, listed)
	case dsTabProperties:
		if d.props.loaded.IsZero() {
			lines = loadingLines(disp, d.props.lastErr, "properties")
			break
		}
		t := propTable{
			props:  d.props.data.d.Properties,
			filter: func(string) bool { return true },
			first:  "PROPERTY", header: "VALUE", bytes: datasetByteProperties, help: zfs.DatasetHelp,
		}
		return t.render(disp, &d.scroll, width, height)
	case dsTabSnapshots:
		lines = d.snapshotLines(disp, v.opts.Now())
	case dsTabPermissions:
		if d.allow.loaded.IsZero() {
			lines = loadingLines(disp, d.allow.lastErr, "permissions")
			break
		}
		lines = permissionLines(disp, d.ds.String(), d.allow.data.delegs, width)
	}
	return scrolled(lines, &d.scroll, height)
}

// scrolled draws the window of lines starting at *scroll, clamping
// *scroll to the content.
func scrolled(lines []string, scroll *int, height int) string {
	*scroll = min(*scroll, max(len(lines)-height, 0))
	end := min(*scroll+height, len(lines))
	return strings.Join(lines[*scroll:end], "\n")
}

// spaceLabel fits "  reservation" and a space.
const spaceLabel = len("  reservation") + 1

// Space gauge glyphs: each part of used space has its own, so the split
// reads without colour.
const (
	glyphData, asciiData         = "█", "#"
	glyphSnapshots, asciiSnaps   = "▓", "="
	glyphChildren, asciiChildren = "▒", "+"
	glyphResv, asciiResv         = "░", "."
)

type spacePart struct {
	label, prop, explain string
	glyph, ascii         string
	tok                  theme.Token
}

// mark is the part's glyph, or its plain character in ASCII mode.
func (p spacePart) mark(ascii bool) string {
	if ascii {
		return p.ascii
	}
	return p.glyph
}

// partsGauge draws d's parts as one stacked gauge, each scaled against
// whole, and returns it with the number of cells drawn.
func partsGauge(disp ui.Display, d zfs.Dataset, parts []spacePart, whole uint64) (string, int) {
	var b strings.Builder
	cells := 0
	for _, p := range parts {
		n, _ := d.Properties.Number(p.prop)
		w := 0
		if whole > 0 {
			w = int((n*uint64(ui.BarWidth) + whole/2) / whole) //nolint:gosec // at most BarWidth
		}
		w = min(w, ui.BarWidth-cells) // rounding must not overflow the gauge
		cells += w
		b.WriteString(disp.Palette.Style(p.tok).Render(strings.Repeat(p.mark(disp.ASCII), w)))
	}
	return b.String(), cells
}

var spaceParts = []spacePart{
	{"data", "usedbydataset", "freed if the dataset were destroyed", glyphData, asciiData, theme.Accent},
	{"snapshots", "usedbysnapshots", "freed by destroying every snapshot", glyphSnapshots, asciiSnaps, theme.Muted},
	{"children", "usedbychildren", "held by datasets below it", glyphChildren, asciiChildren, theme.Muted},
	{"reservation", "usedbyrefreservation", "reserved and not yet written", glyphResv, asciiResv, theme.Muted},
}

// spaceLines breaks used down by reason (zfsprops(7): used is the sum of
// the usedby* properties) with one stacked gauge.
func spaceLines(disp ui.Display, d zfs.Dataset) []string {
	muted := disp.Palette.Style(theme.Muted)
	used, _ := d.Properties.Number("used")
	gauge, _ := partsGauge(disp, d, spaceParts, used)
	size := func(prop string) string {
		n, _ := d.Properties.Number(prop)
		return lpad(format.Size(n, disp.Exact), sizeWidth)
	}
	lines := []string{pad("used", spaceLabel) + size("used") + sp + gauge}
	for _, p := range spaceParts {
		lines = append(lines, pad("  "+p.label, spaceLabel)+size(p.prop)+sp+disp.Palette.Style(p.tok).Render(p.mark(disp.ASCII))+" "+muted.Render(p.prop+": "+p.explain))
	}
	return append(
		lines, "",
		pad("written", spaceLabel)+size("written")+sp+muted.Render("written since the most recent snapshot"),
		pad("available", spaceLabel)+size("available"),
		pad("referenced", spaceLabel)+size("referenced"),
		pad("compression", spaceLabel)+lpad(d.Properties["compressratio"].Text.Display()+"x", sizeWidth),
	)
}

// datasetByteProperties are dataset properties that are byte counts
// (zfsprops(7)).
var datasetByteProperties = map[string]bool{
	"used": true, "available": true, "referenced": true, "logicalused": true, "logicalreferenced": true,
	"quota": true, "refquota": true, "reservation": true, "refreservation": true, "volsize": true,
	"usedbydataset": true, "usedbysnapshots": true, "usedbychildren": true, "usedbyrefreservation": true,
	"written": true, "recordsize": true, "volblocksize": true, "special_small_blocks": true,
}

func (d *dsDetail) snapshotLines(disp ui.Display, now time.Time) []string {
	muted := disp.Palette.Style(theme.Muted)
	switch {
	case d.snaps.loaded.IsZero() && !d.snaps.lastErr.IsEmpty():
		return []string{"Could not read snapshots", d.snaps.lastErr.Display()}
	case d.snaps.loaded.IsZero():
		return []string{muted.Render("Reading snapshots")}
	case len(d.snaps.data.list) == 0:
		return []string{fmt.Sprintf("%s has no snapshots.", d.ds)}
	}
	list := d.order.get(listKey{d.snaps.loaded, len(d.snaps.data.list), d.sortBy, d.reverse}, func() []zfs.SnapshotRow {
		return sortSnapshots(d.snaps.data.list, d.sortBy, d.reverse)
	})
	nameCol := len("SNAPSHOT")
	for _, s := range list {
		nameCol = max(nameCol, len(shortSnap(s.Name)))
	}
	order := "ascending"
	if d.reverse {
		order = "descending"
	}
	lines := []string{
		muted.Render(fmt.Sprintf("%d %s, sorted by %s, %s", len(list), format.Plural(len(list), "snapshot", "snapshots"), sortNames[d.sortBy], order)),
		muted.Render(snapshotHeader(nameCol)),
	}
	for _, s := range list {
		lines = append(lines, snapshotRow(disp, s, shortSnap(s.Name), nameCol, now))
	}
	return lines
}

func snapshotHeader(nameCol int) string {
	return pad("SNAPSHOT", nameCol) + sp + pad("CREATED", len("2006-01-02 15:04")) + sp +
		lpad("USED", sizeWidth) + sp + lpad("REFER", sizeWidth) + sp + lpad("HOLDS", len("HOLDS")) + sp + "CLONES"
}

// sortSnapshots orders by the chosen column. Creation order uses
// createtxg, which is exact where the creation time ties within a second,
// and breaks ties in the other columns.
// sortSnapshots returns a sorted copy of list: by used or referenced
// space if by says so, else by creation.
func sortSnapshots(list []zfs.SnapshotRow, by int, reverse bool) []zfs.SnapshotRow {
	out := slices.Clone(list)
	key := func(s zfs.SnapshotRow) uint64 {
		switch by {
		case sortUsed:
			return s.Used
		case sortReferenced:
			return s.Referenced
		}
		return 0
	}
	slices.SortStableFunc(out, func(a, b zfs.SnapshotRow) int {
		c := cmp.Or(cmp.Compare(key(a), key(b)), cmp.Compare(a.CreateTXG, b.CreateTXG))
		if reverse {
			return -c
		}
		return c
	})
	return out
}

// shortSnap is the part after @: the dataset is in the title line.
func shortSnap(full string) string {
	_, s, _ := strings.Cut(full, "@")
	return "@" + s
}

// snapshotRow draws one snapshot; label is its name as the view shows it.
func snapshotRow(disp ui.Display, s zfs.SnapshotRow, label string, nameCol int, now time.Time) string {
	pal := disp.Palette
	used, refer, holds := s.Used, s.Referenced, s.Holds
	when := time.Unix(int64(s.Creation), 0).In(now.Location()).Format("2006-01-02 15:04") //nolint:gosec // seconds since 1970
	holdText := pal.Style(theme.Muted).Render(lpad("0", len("HOLDS")))
	if holds > 0 {
		holdText = pal.Style(theme.Warn).Render(lpad(fmt.Sprint(holds), len("HOLDS")))
	}
	clones := s.Clones.Display()
	if clones == "" {
		clones = pal.Style(theme.Muted).Render("-")
	}
	return pad(label, nameCol) + sp + when + sp + lpad(format.Size(used, disp.Exact), sizeWidth) + sp +
		lpad(format.Size(refer, disp.Exact), sizeWidth) + sp + holdText + sp + clones
}

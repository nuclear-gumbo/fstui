// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// ARC rates: a file read, every second while the view is shown and every ten
// seconds otherwise, so the graph has data when the user comes back.
const (
	arcShown  = time.Second
	arcHidden = 10 * time.Second
	// arcWindow is how far back the hit ratio graph reaches.
	arcWindow = 5 * time.Minute
	arcView   = 5
)

// ARC shows the ARC: its size against its bounds, the hit ratio over the last
// five minutes, what it holds, and the L2ARC when there are cache devices.
type ARC struct {
	poll poller[zfs.ARCStats]
	now  func() time.Time

	prev   zfs.ARCStats // the reading before the latest, for rates
	prevAt time.Time
	hits   series  // hit ratio per interval, in percent
	sizes  series  // size as a percentage of the maximum, per reading
	l2Now  float64 // L2ARC hit ratio over the last interval
	l2OK   bool
}

// NewARC returns the ARC view reading with read, normally zfs.ReadARCStats.
func NewARC(read func() (zfs.ARCStats, error), now func() time.Time) *ARC {
	return &ARC{
		poll: poller[zfs.ARCStats]{
			read:     func(context.Context) (zfs.ARCStats, error) { return read() },
			interval: arcHidden, now: now,
		},
		now: now,
	}
}

// Name implements ui.Screen.
func (a *ARC) Name() string { return "ARC" }

// Number implements ui.Numbered.
func (a *ARC) Number() int { return arcView }

// Keys implements ui.Screen.
func (a *ARC) Keys() []key.Binding { return []key.Binding{keys.Get(keys.Refresh)} }

// Nested implements ui.Screen.
func (a *ARC) Nested() bool { return false }

// Init implements ui.Screen: reading starts at the background rate.
func (a *ARC) Init() tea.Cmd { return a.poll.start() }

// Activate implements ui.Activator: read now and every second.
func (a *ARC) Activate() tea.Cmd {
	a.poll.interval = arcShown
	return a.poll.refresh()
}

// Deactivate implements ui.Deactivator.
func (a *ARC) Deactivate() tea.Cmd {
	a.poll.interval = arcHidden
	return nil
}

// Status implements ui.Screen.
func (a *ARC) Status() ui.Status {
	return ui.Status{Refreshed: a.poll.loaded, LastError: a.poll.lastErr}
}

// Update implements ui.Screen.
func (a *ARC) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	before := a.poll.loaded
	if cmd, updated := a.poll.handle(msg); cmd != nil || updated {
		if a.poll.loaded != before {
			a.sample(a.poll.data, a.poll.loaded)
		}
		return a, cmd
	}
	if k, ok := msg.(tea.KeyPressMsg); ok && key.Matches(k, keys.Get(keys.Refresh)) {
		return a, a.poll.refresh()
	}
	return a, nil
}

// sample records the rates between the previous reading and cur.
func (a *ARC) sample(cur zfs.ARCStats, at time.Time) {
	from := at.Add(-arcShown)
	if !a.prevAt.IsZero() && at.After(a.prevAt) {
		from = a.prevAt
		if r, ok := zfs.HitRatio(a.prev, cur); ok {
			a.hits.add(a.prevAt, at, r*percent)
		}
		a.l2Now, a.l2OK = zfs.L2HitRatio(a.prev, cur)
	}
	if cur.Max > 0 {
		a.sizes.add(from, at, float64(cur.Size)*percent/float64(cur.Max))
	}
	a.prev, a.prevAt = cur, at
	a.hits.trim(at.Add(-arcWindow))
	a.sizes.trim(at.Add(-arcWindow))
}

const percent = 100

// Render implements ui.Screen.
func (a *ARC) Render(d ui.Display, width, height int) string {
	if a.poll.loaded.IsZero() {
		return strings.Join(loadingLines(d, a.poll.lastErr, "ARC statistics"), "\n")
	}
	s := a.poll.data
	muted := d.Palette.Style(theme.Muted)
	label := func(s string) string { return muted.Render(pad(s, arcLabel)) }
	size := func(n uint64) string { return ui.Size(n, d.Exact) }

	gauge := max(width-arcLabel-len("  3.1 GiB of 7.8 GiB maximum")-arcLabel/2, ui.BarMinWidth)
	head := []string{
		label("size") + ui.Gauge(d, pctOf(s.Size, s.Max), gauge) + sp + size(s.Size) + " of " + size(s.Max) + muted.Render(" maximum"),
		label("") + muted.Render("target ") + size(s.Target) + sp + muted.Render("minimum ") + size(s.Min) +
			sp + muted.Render("memory ") + size(s.Memory) + muted.Render(", ") + available(s, d) + muted.Render(" available"),
		"",
	}
	body := a.contents(d, s, label, size)

	// Two graphs share the rows the rest leaves, three to twelve each:
	// taller adds no detail worth the rows.
	rows := min(max((height-len(head)-len(body)-4)/2, arcGraphMin), arcGraphMax) //nolint:mnd // two graphs, two titles, two gaps
	graphW := max(width-axisWidth, 1)
	now := muted.Render("no reads")
	if v, ok := a.hits.last(); ok {
		now = fmt.Sprintf("%.1f%%", v)
	}
	lines := append(head, muted.Render("size, last 5 minutes"))
	lines = append(lines, axisGraph(d, a.sizes.slots(a.poll.loaded, arcWindow, ui.GraphSamples(d, graphW)), "max  ", graphW, rows)...)
	lines = append(lines, "", muted.Render("hit ratio, last 5 minutes   now ")+now+muted.Render("   since load ")+fmt.Sprintf("%.1f%%", sinceLoad(s)))
	lines = append(lines, axisGraph(d, a.hits.slots(a.poll.loaded, arcWindow, ui.GraphSamples(d, graphW)), "100% ", graphW, rows)...)
	lines = append(lines, "")
	return strings.Join(append(lines, body...), "\n")
}

// axisGraph draws a graph of percentages with its top label (the maximum,
// or 100%) and 0% down the left side.
func axisGraph(d ui.Display, values []float64, topLabel string, width, rows int) []string {
	muted := d.Palette.Style(theme.Muted)
	graph := strings.Split(ui.Graph(d, values, percent, width, rows), "\n")
	for i := range graph {
		axis := strings.Repeat(" ", axisWidth)
		switch i {
		case 0:
			axis = topLabel
		case len(graph) - 1:
			axis = "  0% "
		}
		graph[i] = muted.Render(axis) + graph[i]
	}
	return graph
}

const (
	arcLabel    = 10 // width of the label column
	axisWidth   = len("100% ")
	arcGraphMin = 3
	arcGraphMax = 12
)

// contents lists what the ARC holds, and the L2ARC when present.
func (a *ARC) contents(d ui.Display, s zfs.ARCStats, label func(string) string, size func(uint64) string) []string {
	muted := d.Palette.Style(theme.Muted)
	other := s.Size - min(s.Data+s.Metadata, s.Size)
	lines := []string{
		label("holds") + muted.Render("data ") + size(s.Data) + sp + muted.Render("metadata ") + size(s.Metadata) +
			sp + muted.Render("headers and other ") + size(other),
		label("") + muted.Render("recently used ") + size(s.MRU) + sp + muted.Render("frequently used ") + size(s.MFU),
	}
	if s.Uncompressed > 0 {
		lines = append(lines, label("")+muted.Render("compressed ")+size(s.Compressed)+muted.Render(" of ")+size(s.Uncompressed)+
			fmt.Sprintf(" (%.2fx)", float64(s.Uncompressed)/float64(max(s.Compressed, 1))))
	}
	// The L2 counters outlive the cache devices (they count since the
	// module loaded), so the line follows the device count.
	if s.L2Devices == 0 {
		return lines
	}
	l2 := label("L2ARC") + size(s.L2Size) + muted.Render(" on cache devices   hit ratio now ")
	if a.l2OK {
		l2 += fmt.Sprintf("%.1f%%", a.l2Now*percent)
	} else {
		l2 += muted.Render("no reads")
	}
	if total := s.L2Hits + s.L2Misses; total > 0 {
		l2 += muted.Render("   since load ") + fmt.Sprintf("%.1f%%", float64(s.L2Hits)*percent/float64(total))
	}
	return append(lines, "", l2)
}

// available is the memory the ARC could still grow into; it is negative
// when the ARC must shrink, shown as a warning.
func available(s zfs.ARCStats, d ui.Display) string {
	if s.MemoryFree < 0 {
		return d.Palette.Style(theme.Warn).Render("-" + ui.Size(uint64(-s.MemoryFree), d.Exact))
	}
	return ui.Size(uint64(s.MemoryFree), d.Exact)
}

func sinceLoad(s zfs.ARCStats) float64 {
	r, _ := zfs.HitRatio(zfs.ARCStats{}, s)
	return r * percent
}

func pctOf(n, of uint64) int {
	if of == 0 {
		return 0
	}
	return int(min(n*percent/of, percent)) //nolint:gosec // at most 100
}

// series holds values that each cover an interval of time, oldest first:
// a reading every second while the view is shown, every ten seconds while
// it is not. Drawing samples it on a time axis, so a slower stretch fills
// as many slots as it covers instead of being squeezed.
type series struct{ points []point }

type point struct {
	from, to time.Time
	v        float64
}

func (s *series) add(from, to time.Time, v float64) {
	s.points = append(s.points, point{from, to, v})
}

// trim drops points that ended before t.
func (s *series) trim(t time.Time) {
	i := 0
	for i < len(s.points) && s.points[i].to.Before(t) {
		i++
	}
	s.points = s.points[i:]
}

// last returns the newest value.
func (s *series) last() (float64, bool) {
	if len(s.points) == 0 {
		return 0, false
	}
	return s.points[len(s.points)-1].v, true
}

// means divides the window ending at end into n equal slots, oldest
// first, and gives each the time-weighted mean of the points that
// overlap it, or NaN where none does. Narrower than one point per slot,
// it averages; a sample in the middle would drop the others.
func (s *series) means(end time.Time, window time.Duration, n int) []float64 {
	out := make([]float64, n)
	step := window / time.Duration(max(n, 1))
	for i := range out {
		t0 := end.Add(-window + step*time.Duration(i))
		t1 := t0.Add(step)
		var sum, covered float64
		for _, p := range s.points {
			lo, hi := maxTime(p.from, t0), minTime(p.to, t1)
			if hi.After(lo) {
				w := hi.Sub(lo).Seconds()
				sum += p.v * w
				covered += w
			}
		}
		out[i] = math.NaN()
		if covered > 0 {
			out[i] = sum / covered
		}
	}
	return out
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// slots samples the series at n evenly spaced times over the window
// ending at end, oldest first; a time no point covers is NaN.
func (s *series) slots(end time.Time, window time.Duration, n int) []float64 {
	out := make([]float64, n)
	step := window / time.Duration(max(n, 1))
	j := 0
	for i := range out {
		t := end.Add(-window + step*time.Duration(i) + step/2)
		for j < len(s.points) && !s.points[j].to.After(t) {
			j++
		}
		out[i] = math.NaN()
		if j < len(s.points) && !s.points[j].from.After(t) {
			out[i] = s.points[j].v
		}
	}
	return out
}

// Selection implements ui.Selector: the ARC, whose limits can be changed.
func (a *ARC) Selection() (ui.Target, bool) {
	return ui.Target{Backend: "zfs", Kind: "arc", Names: []string{"ARC"}}, true
}

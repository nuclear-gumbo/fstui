// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

const (
	ioView = 6
	// ioWindow is how far back the peaks in the pool table reach.
	ioWindow = 2 * time.Minute
	// ioKeep is how much history is kept for the charts, which show one
	// second per column: as much of it as the width allows.
	ioKeep = 10 * time.Minute
	// chartRowsMax bounds each chart's height; taller adds no detail
	// worth the rows.
	chartRowsMax = 8
	// ioInterval is zpool iostat's interval: one sample a second.
	ioInterval = time.Second
)

// IOSource streams I/O statistics until ctx is cancelled. *zfs.Reader
// implements it.
type IOSource interface {
	IOStats(ctx context.Context, sample func(zfs.IOSample)) error
}

// IO shows read and write activity: a table of every pool's rates and
// peaks, charts of the selected pool's history, and its devices in the
// last second with their latency. zpool iostat runs for the whole
// session.
type IO struct {
	src IOSource
	now func() time.Time

	gen     int // numbers the running stream; messages from older ones are dropped
	cancel  context.CancelFunc
	ch      chan tea.Msg
	running bool
	lastErr termtext.Text

	latest      zfs.IOSample
	at          time.Time
	read, write map[string]*series // per pool, bytes per second
	cursor      listCursor
}

type (
	ioSampleMsg struct {
		gen    int
		sample zfs.IOSample
		at     time.Time
	}
	ioEndMsg struct {
		gen int
		err error
	}
)

// NewIO returns the I/O view.
func NewIO(src IOSource, now func() time.Time) *IO {
	return &IO{src: src, now: now, read: map[string]*series{}, write: map[string]*series{}}
}

// Name implements ui.Screen.
func (v *IO) Name() string { return "I/O" }

// Number implements ui.Numbered.
func (v *IO) Number() int { return ioView }

// Keys implements ui.Screen.
func (v *IO) Keys() []key.Binding {
	return append([]key.Binding{keys.As(keys.Refresh, "restart zpool iostat")}, listKeys()...)
}

// Nested implements ui.Screen.
func (v *IO) Nested() bool { return false }

// Init implements ui.Screen: zpool iostat starts with fstui and keeps running,
// so the graphs have no gaps from time spent in other views. One process
// giving a sample a second costs little.
func (v *IO) Init() tea.Cmd { return v.start() }

// Activate implements ui.Activator: start zpool iostat again if it
// stopped (an error, a pool imported meanwhile).
func (v *IO) Activate() tea.Cmd {
	if v.running {
		return nil
	}
	return v.start()
}

func (v *IO) start() tea.Cmd {
	v.stop()
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel, v.running, v.lastErr = cancel, true, termtext.Text{}
	gen, ch, src, now := v.gen, make(chan tea.Msg, 1), v.src, v.now
	v.ch = ch
	send := func(m tea.Msg) {
		select {
		case ch <- m:
		case <-ctx.Done():
		}
	}
	go func() {
		defer close(ch)
		err := src.IOStats(ctx, func(s zfs.IOSample) { send(ioSampleMsg{gen: gen, sample: s, at: now()}) })
		send(ioEndMsg{gen: gen, err: err})
	}()
	return next(ch)
}

func (v *IO) stop() {
	if v.cancel != nil {
		v.cancel()
	}
	v.cancel, v.running = nil, false
	v.gen++
}

// next waits for the stream's next message.
func next(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		m, ok := <-ch
		if !ok {
			return nil
		}
		return m
	}
}

// Status implements ui.Screen.
func (v *IO) Status() ui.Status {
	return ui.Status{Refreshed: v.at, LastError: v.lastErr}
}

// Update implements ui.Screen.
func (v *IO) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ioSampleMsg:
		if msg.gen != v.gen {
			return v, nil
		}
		v.record(msg.sample, msg.at)
		return v, next(v.ch)
	case ioEndMsg:
		if msg.gen == v.gen {
			v.running = false
			if msg.err != nil {
				v.lastErr = errText(msg.err)
			}
		}
		return v, nil
	case tea.KeyPressMsg:
		if v.cursor.move(msg, len(v.latest)) {
			return v, nil
		}
		if key.Matches(msg, keys.Get(keys.Refresh)) {
			return v, v.start()
		}
	}
	v.cursor.mouse(msg, len(v.latest), 1) // below the header
	return v, nil
}

func (v *IO) record(s zfs.IOSample, at time.Time) {
	from := at.Add(-ioInterval)
	// Each reading covers the time since the one before, so readings
	// that arrive a little late or early leave no gap between them.
	if !v.at.IsZero() && at.After(v.at) && at.Sub(v.at) < 2*ioInterval {
		from = v.at
	}
	v.latest, v.at = s, at
	seen := map[string]bool{}
	for _, p := range s {
		name := p.Name.Raw()
		seen[name] = true
		for _, x := range []struct {
			m   map[string]*series
			val uint64
		}{{v.read, p.ReadBytes}, {v.write, p.WriteBytes}} {
			if x.m[name] == nil {
				x.m[name] = &series{}
			}
			x.m[name].add(from, at, float64(x.val))
			x.m[name].trim(at.Add(-ioKeep))
		}
	}
	for name := range v.read { // exported pools
		if !seen[name] {
			delete(v.read, name)
			delete(v.write, name)
		}
	}
	v.cursor.clamp(len(s))
}

// Render implements ui.Screen.
func (v *IO) Render(d ui.Display, width, height int) string {
	muted := d.Palette.Style(theme.Muted)
	switch {
	case v.at.IsZero() && !v.lastErr.IsEmpty():
		return "zpool iostat stopped\n" + v.lastErr.Display() + "\n" + muted.Render("r starts it again.")
	case v.at.IsZero():
		return muted.Render("Waiting for the first second of zpool iostat")
	case len(v.latest) == 0:
		return "No pools are imported."
	}
	stopped := 0
	if !v.running {
		stopped = 2
	}
	lines := v.poolLines(d, width, min(len(v.latest)+1, max(height/3, 2)))
	devices := v.deviceLines(d, width, height-len(lines)-stopped)
	// The charts get what the tables leave, the devices giving way
	// first when the charts would be under two rows each.
	room := height - len(lines) - len(devices) - stopped - 2 //nolint:mnd // the blank lines around the charts
	if short := chartMin - room; short > 0 {
		devices = devices[:max(len(devices)-short, 0)]
		room += short
	}
	lines = append(lines, "")
	lines = append(lines, v.chartLines(d, width, room)...)
	if len(devices) > 0 {
		lines = append(lines, "")
		lines = append(lines, devices...)
	}
	if !v.running {
		lines = append(lines, "", muted.Render("zpool iostat stopped; r starts it again."))
	}
	return strings.Join(lines, "\n")
}

const rateWidth = len("1023.9 MiB/s")

// chartMin is the smallest room for the charts: a title, and a label
// row and two rows of bars for each direction.
const chartMin = 1 + 2*(1+2)

// poolLines lists one row per pool: the rates in the last second and
// the peaks over the last two minutes. The selected pool's history is
// drawn below the table (chartLines).
func (v *IO) poolLines(d ui.Display, width, rows int) []string {
	muted := d.Palette.Style(theme.Muted)
	nameCol := len("POOL")
	for _, p := range v.latest {
		nameCol = max(nameCol, lipgloss.Width(p.Name.Display()))
	}
	nameCol = min(nameCol, max(width-markerWidth-4*(gap+rateWidth), len("POOL")))
	head := strings.Repeat(" ", markerWidth) + pad("POOL", nameCol) + sp + lpad("READ", rateWidth) + sp +
		lpad("WRITE", rateWidth) + sp + lpad("READ PEAK", rateWidth) + sp + lpad("WRITE PEAK", rateWidth)
	lines := []string{muted.Render(head)}
	from := v.at.Add(-ioWindow)
	start, end := v.cursor.window(len(v.latest), max(rows-1, 1))
	for i := start; i < end; i++ {
		p := v.latest[i]
		name := p.Name.Raw()
		marker, label := "  ", pad(ansi.Truncate(p.Name.Display(), nameCol, "…"), nameCol)
		if i == v.cursor.i {
			marker = d.Palette.Style(theme.Accent).Render(">") + " "
			label = lipgloss.NewStyle().Bold(true).Render(label)
		}
		lines = append(lines, marker+label+sp+lpad(rate(d, p.ReadBytes), rateWidth)+sp+lpad(rate(d, p.WriteBytes), rateWidth)+sp+
			muted.Render(lpad(rate(d, uint64(v.read[name].peakSince(from))), rateWidth)+sp+
				lpad(rate(d, uint64(v.write[name].peakSince(from))), rateWidth)))
	}
	return lines
}

// chartLines draws the selected pool's read and write history in rows
// lines: one column per second, newest on the right, as far back as the
// width and the kept history allow. Both charts share an axis from zero
// to the larger peak on screen, so their heights compare; that peak is
// printed at the right of each label. Both are in the accent: every
// other colour means a state, and the labels tell them apart.
func (v *IO) chartLines(d ui.Display, width, rows int) []string {
	if rows < chartMin || v.cursor.i >= len(v.latest) || width < 1 {
		return nil
	}
	muted, bold := d.Palette.Style(theme.Muted), lipgloss.NewStyle().Bold(true)
	p := v.latest[v.cursor.i]
	name := p.Name.Raw()
	// As far back as there is history, up to one column per second.
	barW := min(width, int(ioKeep/ioInterval))
	span := time.Duration(barW) * ioInterval
	if r := v.read[name]; r != nil && len(r.points) > 0 {
		span = min(span, max(v.at.Sub(r.points[0].from).Round(ioInterval), ioInterval))
	}
	cols := int(span / ioInterval)
	read := v.read[name].means(v.at, span, cols)
	write := v.write[name].means(v.at, span, cols)
	top := max(peakOf(read), peakOf(write))
	h := min((rows-3)/2, chartRowsMax) //nolint:mnd // a title and two labels; two charts
	label := func(dir string, now uint64) string {
		left := dir + " " + rate(d, now)
		right := "peak " + rate(d, uint64(top))
		return left + strings.Repeat(" ", max(width-len(left)-len(right), 1)) + muted.Render(right)
	}
	style := d.Palette.Style(theme.Accent)
	return []string{
		bold.Render(p.Name.Display()) + muted.Render(", last "+ago(span)),
		label("read ", p.ReadBytes),
		ui.Bars(d, read, top, barW, h, style), // newest at the right edge
		label("write", p.WriteBytes),
		ui.Bars(d, write, top, barW, h, style),
	}
}

// ago says how long span is: "45 s", "2 min", "2 min 30 s".
func ago(span time.Duration) string {
	m, s := int(span/time.Minute), int(span%time.Minute/time.Second)
	switch {
	case m == 0:
		return fmt.Sprintf("%d s", s)
	case s == 0:
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d min %d s", m, s)
}

// peakOf is the largest value, ignoring missing ones (NaN).
func peakOf(values []float64) float64 {
	m := 0.0
	for _, x := range values {
		if x > m {
			m = x
		}
	}
	return m
}

// deviceLines lists the selected pool's devices in the last second.
func (v *IO) deviceLines(d ui.Display, width, rows int) []string {
	if rows < 2 || v.cursor.i >= len(v.latest) {
		return nil
	}
	muted := d.Palette.Style(theme.Muted)
	p := v.latest[v.cursor.i]
	const opsWidth, waitWidth = len("WRITE OPS"), len("WRITE WAIT")
	fixed := 2*(gap+opsWidth) + 2*(gap+rateWidth) + 2*(gap+waitWidth)
	nameCol := max(width-fixed, len("DEVICE"))
	head := pad("DEVICE", nameCol) + sp + lpad("READ OPS", opsWidth) + sp + lpad("WRITE OPS", opsWidth) + sp +
		lpad("READ", rateWidth) + sp + lpad("WRITE", rateWidth) + sp + lpad("READ WAIT", waitWidth) + sp + lpad("WRITE WAIT", waitWidth)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(p.Name.Display()) + muted.Render(" devices, last second"),
		muted.Render(head),
	}
	section := ""
	for _, dev := range p.Devices {
		if len(lines) >= rows {
			break
		}
		if dev.Section != section {
			section = dev.Section
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render(section))
		}
		name := strings.Repeat(" ", (dev.Depth-1)*indent) + leftTrunc(dev.Name.Display(), nameCol-(dev.Depth-1)*indent)
		lines = append(lines, pad(name, nameCol)+sp+lpad(fmt.Sprint(dev.ReadOps), opsWidth)+sp+
			lpad(fmt.Sprint(dev.WriteOps), opsWidth)+sp+lpad(rate(d, dev.ReadBytes), rateWidth)+sp+
			lpad(rate(d, dev.WriteBytes), rateWidth)+sp+lpad(wait(d, dev.ReadWait, dev.HasReadWait), waitWidth)+sp+
			lpad(wait(d, dev.WriteWait, dev.HasWriteWait), waitWidth))
	}
	return lines
}

func rate(d ui.Display, n uint64) string { return ui.Size(n, d.Exact) + "/s" }

// wait formats a latency; "-" when there was no I/O to measure.
func wait(d ui.Display, t time.Duration, ok bool) string {
	switch {
	case !ok:
		return "-"
	case t < time.Millisecond:
		micro := "µs"
		if d.ASCII {
			micro = "us"
		}
		return fmt.Sprintf("%d %s", t.Microseconds(), micro)
	case t < time.Second:
		return fmt.Sprintf("%.1f ms", float64(t)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.2f s", t.Seconds())
}

// peakSince is the largest value of the points that end after t; nil
// and empty series give 0.
func (s *series) peakSince(t time.Time) float64 {
	if s == nil {
		return 0
	}
	m := 0.0
	for _, p := range s.points {
		if p.to.After(t) {
			m = max(m, p.v)
		}
	}
	return m
}

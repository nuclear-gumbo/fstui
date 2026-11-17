// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/tune"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// TuneSource is what the Tune tab reads. *zfs.Reader implements it; a
// Source without it shows the tab as unavailable.
type TuneSource interface {
	ReadTuneSample(pool name.Pool, now time.Time) (zfs.TuneSample, error)
	TuneIO(ctx context.Context, pool name.Pool, every time.Duration, report func(zfs.IOPool)) error
	Get(ctx context.Context, names, props []string) (map[string]map[string]string, error)
}

// tuneEvery is how often the Tune tab reads kstats and zpool iostat
// reports an interval.
const tuneEvery = 5 * time.Second

// tuneState is the Tune tab's measuring: started when the tab is shown,
// stopped when the user leaves it, the pool or the view.
type tuneState struct {
	gen      int
	cancel   context.CancelFunc
	win      tune.Window
	records  map[string]uint64 // recordsize or volblocksize per dataset
	compress float64           // the pool's compression ratio
	comp     []tune.DatasetCompression
	volumes  map[string]bool
	roots    []string // mounted filesystems, for the file walk
	walk     *tune.WalkResult
	walking  context.CancelFunc
	err      termtext.Text
	evidence bool // enter shows the evidence under each finding
	ch       chan tea.Msg
	src      TuneSource
}

type (
	tuneSampleMsg struct {
		gen    int
		sample zfs.TuneSample
		err    error
	}
	tuneTickMsg struct{ gen int }
	tuneIOMsg   struct {
		gen int
		rep zfs.IOPool
	}
	tuneRecordsMsg struct {
		gen      int
		records  map[string]uint64
		compress float64
		comp     []tune.DatasetCompression
		roots    []string
		volumes  map[string]bool
		err      error
	}
	tuneWalkMsg struct {
		gen    int
		result *tune.WalkResult
	}
	tuneEndMsg struct {
		gen int
		err error
	}
)

// startTune starts measuring the open pool afresh.
func (p *Pools) startTune() tea.Cmd {
	d := p.detail
	p.stopTune()
	src, ok := p.src.(TuneSource)
	if !ok {
		return nil
	}
	p.tuneGen++
	gen := p.tuneGen
	ctx, cancel := context.WithCancel(context.Background())
	evidence := d.tune != nil && d.tune.evidence
	ch := make(chan tea.Msg, 8)
	d.tune = &tuneState{gen: gen, cancel: cancel, evidence: evidence, ch: ch, src: src}
	pool, now := d.pool, p.opts.Now
	send := func(m tea.Msg) {
		select {
		case ch <- m:
		case <-ctx.Done():
		}
	}
	go func() {
		defer close(ch)
		err := src.TuneIO(ctx, pool, tuneEvery, func(r zfs.IOPool) { send(tuneIOMsg{gen: gen, rep: r}) })
		send(tuneEndMsg{gen: gen, err: err})
	}()
	sample := func() tea.Msg {
		s, err := src.ReadTuneSample(pool, now())
		return tuneSampleMsg{gen: gen, sample: s, err: err}
	}
	return tea.Batch(sample, next(ch))
}

// stopTune ends the open pool's measuring and file walk, if any.
func (p *Pools) stopTune() {
	if d := p.detail; d != nil && d.tune != nil && d.tune.cancel != nil {
		d.tune.cancel()
		d.tune.cancel = nil
		if d.tune.walking != nil {
			d.tune.walking()
		}
	}
}

// measureFiles starts the per-file compression walk of the pool's
// mounted filesystems; it ends with the tab or at its limits.
func (p *Pools) measureFiles() tea.Cmd {
	t := p.detail.tune
	if t == nil || t.cancel == nil || t.walking != nil || len(t.roots) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.walking, t.walk = cancel, nil
	gen, roots := t.gen, t.roots
	return func() tea.Msg { return tuneWalkMsg{gen: gen, result: tune.Walk(ctx, roots)} }
}

// updateTune handles the Tune tab's messages; ok is false for others.
// Messages from a stream that was stopped are dropped.
func (p *Pools) updateTune(msg tea.Msg) (tea.Cmd, bool) {
	var t *tuneState
	if d := p.detail; d != nil {
		t = d.tune
	}
	live := func(gen int) bool { return t != nil && t.gen == gen && t.cancel != nil }
	switch msg := msg.(type) {
	case tuneSampleMsg:
		if !live(msg.gen) {
			return nil, true
		}
		return p.tuneSample(t, msg), true
	case tuneTickMsg:
		if !live(msg.gen) {
			return nil, true
		}
		src, pool, now := t.src, p.detail.pool, p.opts.Now
		return func() tea.Msg {
			s, err := src.ReadTuneSample(pool, now())
			return tuneSampleMsg{gen: msg.gen, sample: s, err: err}
		}, true
	case tuneIOMsg:
		if !live(msg.gen) {
			return nil, true // a stopped stream's last report
		}
		t.win.AddIO(msg.rep, tuneEvery.Seconds())
		return next(t.ch), true
	case tuneRecordsMsg:
		if live(msg.gen) {
			t.records, t.compress, t.comp, t.roots, t.volumes = msg.records, msg.compress, msg.comp, msg.roots, msg.volumes
			t.setErr(msg.err)
		}
		return nil, true
	case tuneWalkMsg:
		if live(msg.gen) {
			t.walk, t.walking = msg.result, nil
		}
		return nil, true
	case tuneEndMsg:
		if live(msg.gen) {
			t.setErr(msg.err)
		}
		return nil, true
	}
	return nil, false
}

// tuneSample adds a kstat reading; the first also reads the record sizes.
func (p *Pools) tuneSample(t *tuneState, msg tuneSampleMsg) tea.Cmd {
	if msg.err != nil {
		t.setErr(msg.err)
		return p.tuneTick(msg.gen)
	}
	first := t.win.Start.At.IsZero()
	t.win.Sample(msg.sample)
	if first {
		return tea.Batch(p.tuneTick(msg.gen), t.readRecords(msg.gen, p.detail.pool.String(), msg.sample))
	}
	return p.tuneTick(msg.gen)
}

func (t *tuneState) setErr(err error) {
	if err != nil {
		t.err = errText(err)
	}
}

func (p *Pools) tuneTick(gen int) tea.Cmd {
	return tea.Tick(tuneEvery, func(time.Time) tea.Msg { return tuneTickMsg{gen: gen} })
}

// readRecords reads the record size of every dataset in the sample, and
// the pool's compression ratio from its root dataset.
func (t *tuneState) readRecords(gen int, pool string, s zfs.TuneSample) tea.Cmd {
	src := t.src
	var names []string
	for ds := range s.Objsets {
		names = append(names, ds)
	}
	return func() tea.Msg {
		got, err := src.Get(context.Background(), names, []string{"recordsize", "volblocksize", "compressratio", "compression", "used", "logicalused", "mountpoint", "mounted"})
		ratio := func(ds string) float64 {
			r, _ := strconv.ParseFloat(strings.TrimSuffix(got[ds]["compressratio"], "x"), 64)
			return r
		}
		compress := ratio(pool)
		var comp []tune.DatasetCompression
		var roots []string
		for _, ds := range names {
			props := got[ds]
			used, _ := strconv.ParseUint(props["used"], 10, 64)
			logical, _ := strconv.ParseUint(props["logicalused"], 10, 64)
			// used includes children; the walk and the verdict are per
			// dataset, so this is a rough guide for nested datasets.
			comp = append(comp, tune.DatasetCompression{Dataset: ds, Algorithm: props["compression"], Ratio: ratio(ds), Used: used, LogicalUsed: logical})
			if mp := props["mountpoint"]; props["mounted"] == "yes" && strings.HasPrefix(mp, "/") {
				roots = append(roots, mp)
			}
		}
		records, volumes := map[string]uint64{}, map[string]bool{}
		for ds, props := range got {
			if n, perr := strconv.ParseUint(props["recordsize"], 10, 64); perr == nil && n > 0 {
				records[ds] = n
			}
			if n, perr := strconv.ParseUint(props["volblocksize"], 10, 64); perr == nil && n > 0 {
				records[ds], volumes[ds] = n, true
			}
		}
		return tuneRecordsMsg{gen: gen, records: records, compress: compress, comp: comp, roots: roots, volumes: volumes, err: err}
	}
}

// tuneLines draws the Tune tab at width.
func (p *Pools) tuneLines(disp ui.Display, width int) []string {
	muted := disp.Palette.Style(theme.Muted)
	t := p.detail.tune
	if _, ok := p.src.(TuneSource); !ok || t == nil {
		return []string{muted.Render("Tune mode is not available here.")}
	}
	if !t.err.IsEmpty() {
		return []string{"Could not measure " + p.detail.pool.String(), t.err.Display()}
	}
	w := &t.win
	if w.Start.At.IsZero() {
		return []string{muted.Render("Starting to measure " + p.detail.pool.String())}
	}
	head := fmt.Sprintf("Measured over %s; r starts again, enter shows the evidence.", ui.Age(w.Now.At.Sub(w.Start.At)))
	lines := []string{muted.Render(head)}
	if t.walking != nil {
		lines = append(lines, muted.Render("Reading file metadata for the compression question; leaving the tab stops it."))
	}
	if w.Restarted {
		lines = append(lines, muted.Render("Measuring started again: "+p.detail.pool.String()+"'s counters were reset (an export and import)."))
	}
	for _, f := range p.findings(func(n uint64) string { return ui.Size(n, disp.Exact) }) {
		lines = append(lines, "", verdictStyle(disp, f.Verdict).Render(f.Verdict.Word())+"  "+f.Question)
		lines = append(lines, indented(termtext.New(f.Figure).Display(), width, 2, nil)...)
		lines = append(lines, indented(termtext.New(f.Points).Display(), width, 2, &muted)...)
		if f.Change != nil {
			lines = append(lines, indented(muted.Render("a offers this change"), width, 2, nil)...)
		}
		if t.evidence {
			for _, e := range f.Evidence {
				lines = append(lines, indented(termtext.New(e).Display(), width, 4, nil)...)
			}
		}
	}
	return lines
}

func verdictStyle(d ui.Display, v tune.Verdict) lipgloss.Style {
	switch v {
	case tune.Fits:
		return d.Palette.Style(theme.OK)
	case tune.WorthALook:
		return d.Palette.Style(theme.Warn)
	case tune.Mismatch:
		return d.Palette.Style(theme.Error)
	case tune.NotEnough:
	}
	return d.Palette.Style(theme.Muted)
}

// indented wraps s to width with every line indented by n, in style if
// one is given.
func indented(s string, width, n int, style *lipgloss.Style) []string {
	pad := strings.Repeat(" ", n)
	out := wrapIndented(s, max(width-n, 10), 0)
	for i, l := range out {
		if style != nil {
			l = style.Render(l)
		}
		out[i] = pad + l
	}
	return out
}

// findings answers the five questions from the open pool's window.
func (p *Pools) findings(size func(uint64) string) []tune.Finding {
	t := p.detail.tune
	w := &t.win
	var layout tune.Layout
	if o, ok := p.overview(); ok {
		layout = tune.LayoutOf(o.Status)
	}
	amp := tune.WriteAmplification(w, layout, t.compress, size)
	cost := tune.Cost{Writes: amp.Verdict >= tune.WorthALook, Reads: tune.ReadAmplified(w, layout)}
	return []tune.Finding{
		amp, tune.Sizes(w, t.records, t.volumes, cost, size), tune.ARC(w, size),
		tune.Saturation(w, layout), tune.Compression(t.comp, t.walk, size),
	}
}

// tuneChanges are the changes the findings point to, encoded for the
// change menu.
func (p *Pools) tuneChanges() []string {
	if p.detail.tune.win.Start.At.IsZero() {
		return nil
	}
	var out []string
	for _, f := range p.findings(func(n uint64) string { return strconv.FormatUint(n, 10) }) {
		if c := f.Change; c != nil {
			out = append(out, c.Encode())
		}
	}
	return out
}

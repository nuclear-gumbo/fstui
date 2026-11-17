// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// propertiesInterval: pool properties rarely change.
const propertiesInterval = 60 * time.Second

// Tabs of pool detail.
const (
	tabTopology = iota
	tabSpace
	tabProperties
	tabFeatures
	tabHistory
	tabTune
	numTabs
)

var tabNames = []string{"Topology", "Capacity", "Properties", "Features", "History", "Tune"}

// detail is the open pool. Its topology comes from the overview poll,
// which already reads zpool status for every pool; properties and
// features need zpool get.
type detail struct {
	pool     name.Pool
	tab      int
	scroll   int
	device   int // Topology: the selected device, among leafNames
	props    *zfs.Pool
	propsAt  time.Time
	propsErr termtext.Text
	loading  bool
	timer    int

	space poller[poolSpace] // started when the Space tab is first shown

	history     zfs.History
	historyGen  int // counts reads, for historyView
	historyView derived[historyKey, []string]
	historyErr  termtext.Text
	historyRead bool // a read finished, successfully or not
	internal    bool // i: include internal events

	tune *tuneState // measuring while the Tune tab is shown
}

type (
	propsMsg struct {
		pool  name.Pool
		props zfs.Pool
		err   error
		at    time.Time
	}
	propsPollMsg struct {
		pool  name.Pool
		timer int
	}
	historyMsg struct {
		pool    name.Pool
		history zfs.History
		err     error
	}
)

// loadHistory reads zpool history when the tab is shown; history only
// grows, so it is read again on r, never on a timer.
func (p *Pools) loadHistory() tea.Cmd {
	d := p.detail
	if d == nil || !p.opts.Root {
		return nil
	}
	src, pool := p.src, d.pool
	return func() tea.Msg {
		h, err := src.PoolHistory(context.Background(), pool)
		return historyMsg{pool: pool, history: h, err: err}
	}
}

func (p *Pools) applyHistory(msg historyMsg) {
	d := p.detail
	if d == nil || d.pool != msg.pool {
		return
	}
	d.historyRead = true
	if msg.err != nil {
		d.historyErr = errText(msg.err)
		return
	}
	d.history, d.historyErr = msg.history, termtext.Text{}
	d.historyGen++
}

// historyKey is what the History tab's lines depend on: the read, the
// internal toggle and the theme (a styled sample).
type historyKey struct {
	gen      int
	internal bool
	style    string
}

func (p *Pools) historyLines(disp ui.Display) []string {
	d := p.detail
	muted := disp.Palette.Style(theme.Muted)
	switch {
	case !p.opts.Root:
		return []string{
			"History needs root: zpool history refuses other users (permission denied).",
			muted.Render("Run fstui as root to see this pool's command history."),
		}
	case !d.historyRead:
		return []string{muted.Render("Reading history")}
	case !d.historyErr.IsEmpty():
		return []string{"Could not read history", d.historyErr.Display()}
	}
	key := historyKey{gen: d.historyGen, internal: d.internal, style: muted.Render(".")}
	return d.historyView.get(key, func() []string { return historyText(d.history, d.internal, muted) })
}

// historyText is the History tab's text, newest first: user commands, and
// internal events too if internal is set.
func historyText(h zfs.History, internal bool, muted lipgloss.Style) []string {
	head := fmt.Sprintf("%d user commands, newest first; i shows internal events", h.UserTotal)
	es := h.User
	if internal {
		head = fmt.Sprintf("%d user commands and %d internal events, newest first; i hides internal events", h.UserTotal, h.InternalTotal)
		es = mergeHistory(h.User, h.Internal)
	}
	lines := []string{muted.Render(head)}
	if len(h.User) < h.UserTotal || internal && len(h.Internal) < h.InternalTotal {
		lines = append(lines, muted.Render(fmt.Sprintf("Showing the most recent %d of each.", max(len(h.User), len(h.Internal)))))
	}
	for i := len(es) - 1; i >= 0; i-- {
		e := es[i]
		when := muted.Render(e.Time.Format("2006-01-02 15:04:05")) + sp
		text := e.Text.FirstLine().Display()
		if e.Internal {
			text = muted.Render(text)
		}
		lines = append(lines, when+text)
	}
	return lines
}

// mergeHistory merges two runs of entries, each in zpool's order, into one.
func mergeHistory(a, b []zfs.HistoryEntry) []zfs.HistoryEntry {
	out := make([]zfs.HistoryEntry, 0, len(a)+len(b))
	for len(a) > 0 && len(b) > 0 {
		if a[0].Seq < b[0].Seq {
			out, a = append(out, a[0]), a[1:]
		} else {
			out, b = append(out, b[0]), b[1:]
		}
	}
	return append(append(out, a...), b...)
}

// open moves focus into the selected pool's detail.
func (p *Pools) open() tea.Cmd {
	cmd := p.follow()
	if p.detail != nil {
		p.focus = true
	}
	return cmd
}

// follow makes the detail show the selected pool, keeping the tab, and
// starts what that tab needs to read. In the split layout it runs on
// every selection change; otherwise when a pool is opened.
func (p *Pools) follow() tea.Cmd {
	if p.cursor >= len(p.poll.data) {
		return nil
	}
	pool := p.poll.data[p.cursor].Pool.Name
	if p.detail != nil && p.detail.pool == pool {
		return nil
	}
	tab := tabTopology
	if p.detail != nil {
		tab = p.detail.tab
		p.stopTune()
	}
	p.detail = &detail{pool: pool, tab: tab, space: newSpacePoller(p.src, pool, p.opts)}
	return p.enterTab()
}

func (p *Pools) loadProps() tea.Cmd {
	d := p.detail
	if d == nil || d.loading {
		return nil
	}
	d.loading = true
	src, pool, now := p.src, d.pool, p.opts.Now
	return func() tea.Msg {
		props, err := src.PoolProperties(context.Background(), pool)
		return propsMsg{pool: pool, props: props, err: err, at: now()}
	}
}

func (p *Pools) applyProps(msg propsMsg) tea.Cmd {
	d := p.detail
	if d == nil || d.pool != msg.pool {
		return nil // the user left this pool while the read ran
	}
	d.loading = false
	if msg.err != nil {
		d.propsErr = errText(msg.err)
	} else {
		d.props, d.propsAt, d.propsErr = &msg.props, msg.at, termtext.Text{}
	}
	d.timer++
	pool, timer := d.pool, d.timer
	return tea.Tick(propertiesInterval, func(time.Time) tea.Msg { return propsPollMsg{pool: pool, timer: timer} })
}

func (p *Pools) detailKey(msg tea.KeyPressMsg) tea.Cmd {
	d := p.detail
	switch {
	case key.Matches(msg, keys.As(keys.Back, "back")):
		p.focus = false
		if !p.geo.split {
			p.stopTune()
			p.detail = nil
		}
	case key.Matches(msg, keys.As(keys.Right, "next tab")):
		d.tab, d.scroll = (d.tab+1)%numTabs, 0
		return p.enterTab()
	case key.Matches(msg, keys.As(keys.Left, "previous tab")):
		d.tab, d.scroll = (d.tab+numTabs-1)%numTabs, 0
		return p.enterTab()
	case key.Matches(msg, keys.Get(keys.MeasureFiles)) && d.tab == tabTune:
		return p.measureFiles()
	case key.Matches(msg, keys.As(keys.Select, "evidence")) && d.tab == tabTune && d.tune != nil:
		d.tune.evidence = !d.tune.evidence
	case key.Matches(msg, keys.Get(keys.Internal)) && d.tab == tabHistory:
		d.internal, d.scroll = !d.internal, 0
	case d.tab == tabTopology && key.Matches(msg, keys.Get(keys.Down)):
		d.device++ // clamped when rendering, where the devices are known
	case d.tab == tabTopology && key.Matches(msg, keys.Get(keys.Up)):
		d.device = max(d.device-1, 0)
	case key.Matches(msg, keys.As(keys.Down, "scroll")):
		d.scroll++ // clamped when rendering, where the content height is known
	case key.Matches(msg, keys.As(keys.Up, "scroll back")):
		d.scroll = max(d.scroll-1, 0)
	case key.Matches(msg, keys.Get(keys.Refresh)):
		d.timer++
		cmds := []tea.Cmd{p.poll.refresh(), p.loadProps()}
		switch d.tab {
		case tabTune:
			cmds = append(cmds, p.startTune())
		case tabHistory:
			cmds = append(cmds, p.loadHistory())
		case tabSpace:
			cmds = append(cmds, d.space.refresh())
		}
		return tea.Batch(cmds...)
	}
	return nil
}

// enterTab starts a tab's reads the first time it is shown; Tune
// measures only while it is shown.
func (p *Pools) enterTab() tea.Cmd {
	d := p.detail
	if d.tab != tabTune {
		p.stopTune()
	}
	switch {
	case d.tab == tabTune:
		return p.startTune()
	case (d.tab == tabProperties || d.tab == tabFeatures) && d.props == nil && !d.loading:
		return p.loadProps()
	case d.tab == tabHistory && !d.historyRead:
		return p.loadHistory()
	case d.tab == tabSpace && d.space.loaded.IsZero() && !d.space.inFlight:
		return d.space.start()
	}
	return nil
}

// overview returns the open pool's latest overview, or false once the pool
// has gone (exported or destroyed).
func (p *Pools) overview() (zfs.Overview, bool) {
	for _, o := range p.poll.data {
		if o.Pool.Name == p.detail.pool {
			return o, true
		}
	}
	return zfs.Overview{}, false
}

func (p *Pools) renderDetail(disp ui.Display, width, height int) string {
	d := p.detail
	o, ok := p.overview()
	if !ok {
		return fmt.Sprintf("Pool %s is no longer imported.\n", d.pool) +
			disp.Palette.Style(theme.Muted).Render("esc returns to the pool list.")
	}
	var lines []string
	switch d.tab {
	case tabTopology:
		var cur int
		lines, cur = p.topology(disp, o, width)
		if p.focus && cur >= 0 { // keep the selected device on screen
			d.scroll = min(d.scroll, cur)
			d.scroll = max(d.scroll, cur-height+1)
		}
	case tabSpace:
		if d.space.loaded.IsZero() {
			lines = loadingLines(disp, d.space.lastErr, "datasets")
			break
		}
		lines = spaceRanking(disp, o, d.space.data.list, thresholdsView{p.opts.Thresholds.CapacityWarn, p.opts.Thresholds.CapacityError}, width)
	case tabProperties, tabFeatures:
		if d.props == nil {
			lines = loadingLines(disp, d.propsErr, "properties")
			break
		}
		features := d.tab == tabFeatures
		t := propTable{
			props:  d.props.Properties,
			filter: func(n string) bool { return strings.HasPrefix(n, "feature@") == features },
			first:  "PROPERTY", header: "VALUE", bytes: byteProperties, help: zfs.PoolHelp,
		}
		if features {
			t.first, t.header = "FEATURE", "STATE"
		}
		return t.render(disp, &d.scroll, width, height)
	case tabHistory:
		lines = p.historyLines(disp)
	case tabTune:
		lines = p.tuneLines(disp, width)
	}
	return scrolled(lines, &d.scroll, height)
}

// detailPanel frames the open pool's detail, with its tabs in the top
// edge.
func (p *Pools) detailPanel(disp ui.Display, width, height int, focused bool) string {
	iw, ih := ui.Inner(width, height)
	content := p.renderDetail(disp, iw, ih-summaryLines)
	if o, ok := p.overview(); ok {
		content = p.summary(disp, o, iw) + "\n" + content
	}
	return p.detailFrame(focused).Render(disp, content, width, height)
}

// summaryLines is the pool summary at the top of the detail panel: two
// lines and a blank one.
const summaryLines = 3

// summary keeps the overview's figures in view when the pool list is
// narrowed to a side panel: health, capacity, then fragmentation, dedup,
// last scrub and device errors.
func (p *Pools) summary(d ui.Display, o zfs.Overview, width int) string {
	pal := d.Palette
	th := p.opts.Thresholds
	pct := capacityPct(o)
	level := ui.LevelOf(pct, th.CapacityWarn, th.CapacityError)
	word := ui.LevelWord(level)
	if word != "" {
		word = " " + pal.Style(map[ui.Level]theme.Token{ui.Warning: theme.Warn, ui.Critical: theme.Error}[level]).Render(word)
	}
	bar := ""
	if width >= ui.BarWidth+summaryText {
		bar = ui.Bar(d, pct, ui.BarWidth, th.CapacityWarn, th.CapacityError) + sp
	}
	first := pal.Style(stateToken(o.Status.State)).Render(o.Status.State) + sp + bar +
		fmt.Sprintf("%d%%", pct) + word + sp + ui.Size(o.Used, d.Exact) + " of " + ui.Size(o.Used+o.Available, d.Exact)
	muted := pal.Style(theme.Muted)
	second := muted.Render("frag ") + fragText(o) + sp + muted.Render("dedup ") +
		o.Pool.Properties["dedupratio"].Text.Display() + "x" + sp + scanSummary(d, o, p.opts.Now) +
		p.nextScrub(d, o) + sp + muted.Render("errors ") + fmt.Sprint(deviceErrors(o.Status))
	return first + "\n" + second + "\n"
}

// nextScrub is when the pool is scrubbed next, for the summary.
func (p *Pools) nextScrub(d ui.Display, o zfs.Overview) string {
	muted := d.Palette.Style(theme.Muted)
	s, scheduled, known := p.schedule(o.Pool.Name.String())
	switch {
	case !known:
		return ""
	case !scheduled:
		return sp + muted.Render("no scrub scheduled")
	case s.Next.IsZero():
		return sp + muted.Render("next scrub scheduled")
	}
	return sp + muted.Render("next ") + "in " + ui.Age(s.Next.Sub(p.opts.Now()))
}

// summaryText is room for the summary's figures beside its bar.
const summaryText = 40

func (p *Pools) detailFrame(focused bool) ui.Panel {
	return ui.Panel{Title: p.detail.pool.String(), Tabs: tabNames, Active: p.detail.tab, Focused: focused}
}

// byteProperties are pool properties that are byte counts (zpoolprops(7)).
var byteProperties = map[string]bool{
	"size": true, "allocated": true, "free": true, "freeing": true, "leaked": true,
	"expandsize": true, "checkpoint": true, "bcloneused": true, "bclonesaved": true,
	"dedup_table_size": true, "dedup_table_quota": true,
}

// loadingLines is shown until a read succeeds, or with its error.
func loadingLines(d ui.Display, err termtext.Text, what string) []string {
	if !err.IsEmpty() {
		return []string{"Could not read " + what, err.Display()}
	}
	return []string{d.Palette.Style(theme.Muted).Render("Reading " + what)}
}

func propertyRow(d ui.Display, n string, v zfs.Property, nameCol, srcCol, width int, bytes map[string]bool) string {
	pal := d.Palette
	label := pad(strings.TrimPrefix(n, "feature@"), nameCol) + sp
	if strings.HasPrefix(n, "feature@") {
		switch v.Text.Raw() {
		case "active":
			return label + pal.Style(theme.OK).Render("active")
		case "disabled":
			return label + pal.Style(theme.Muted).Render("disabled")
		}
		return label + v.Text.Display() // enabled, or a state fstui does not know
	}
	val := propertyValue(d, n, v, bytes)
	src := sourceText(v.Source)
	// The value gets what the name and the source column leave.
	valCol := max(width-nameCol-gap-gap-srcCol, 1)
	return label + pad(ansi.Truncate(val, valCol, "…"), valCol) + sp + pal.Style(theme.Muted).Render(leftTrunc(src, srcCol))
}

// Properties whose numbers need a unit or a form to be read: zfs get
// prints these with % or x, as a date, or as "none" without -p
// (zpoolprops(7), zfsprops(7)); fstui reads them with -p for exact
// numbers and formats them here.
var (
	percentProperties = map[string]bool{"capacity": true, "fragmentation": true}
	ratioProperties   = map[string]bool{"dedupratio": true, "bcloneratio": true, "compressratio": true, "refcompressratio": true}
	timeProperties    = map[string]bool{"creation": true}
	// noneWhenZero: a zero quota or reservation means there is none.
	noneWhenZero = map[string]bool{"quota": true, "refquota": true, "reservation": true, "refreservation": true}
)

// propertyValue is a property's value as the Properties tab shows it.
func propertyValue(d ui.Display, n string, v zfs.Property, bytes map[string]bool) string {
	text := v.Text.Display()
	switch {
	case !v.IsNumber && ratioProperties[n] && text != "" && text != "-":
		return text + "x"
	case !v.IsNumber:
		return text
	case noneWhenZero[n] && v.Number == 0:
		return "none"
	case bytes[n]:
		return ui.Size(v.Number, d.Exact)
	case percentProperties[n]:
		return fmt.Sprintf("%d%%", v.Number)
	case ratioProperties[n]:
		return text + "x"
	case timeProperties[n]:
		return time.Unix(int64(v.Number), 0).Local().Format("2006-01-02 15:04") //nolint:gosec // seconds since 1970
	}
	return text
}

// sourceText says where a value comes from, as zfs get's SOURCE column
// does: "local", "default", "inherited from tank/home", "received".
func sourceText(s zfs.Source) string {
	switch s.Kind {
	case zfs.SourceNone:
		return ""
	case zfs.SourceInherited:
		return "inherited from " + s.From().Display()
	case zfs.SourceDefault, zfs.SourceLocal, zfs.SourceTemporary, zfs.SourceReceived, zfs.SourceOther:
	}
	return strings.ToLower(termtext.New(s.Word()).Display())
}

// scanSummary is the summary's last scan: its kind muted, then its state.
func scanSummary(d ui.Display, o zfs.Overview, now func() time.Time) string {
	word, state := scanParts(o, now)
	return d.Palette.Style(theme.Muted).Render(word+" ") + state
}

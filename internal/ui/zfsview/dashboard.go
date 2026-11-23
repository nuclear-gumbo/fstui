// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// dashboardView is the 0 key.
const dashboardView = 10

// DashboardOptions configures the dashboard.
type DashboardOptions struct {
	Now      func() time.Time
	Settings *config.Config // shared by the views; nil: defaults
	// Hosts lists the other hosts for the hosts panel; nil without the
	// remote module.
	Hosts *ui.HostLister
}

// Dashboard is a read-only overview meant to stay on a screen all day: the
// health of every pool, recent warnings and errors, I/O, the ARC and running
// scans. It reads nothing itself: it draws the data of the views it is made
// of, and while it is shown it asks them to read at their shown rates. It
// never offers an action.
type Dashboard struct {
	pools *Pools
	evs   *Events
	io    *IO
	arc   *ARC
	opts  DashboardOptions

	// What the user has seen: problems that appear after the last key
	// press mark their panel until the next one, so something that
	// happened at night is still flagged in the morning.
	seenErrors, seenWarnings, seenProblems int

	focus    int        // index into dashboard.panels of the panel keys go to
	evCursor int        // selected row of the events panel
	evFirst  int        // its first row drawn
	evShown  []uint64   // event IDs in the events panel, newest first
	rects    []panelBox // where the last render put each panel
	hosts    hostsPanel
}

// panelBox is where a panel was drawn, for the mouse.
type panelBox struct {
	name       string
	x, y, w, h int
}

// NewDashboard returns the dashboard over the given views.
func NewDashboard(pools *Pools, evs *Events, io *IO, arc *ARC, o DashboardOptions) *Dashboard {
	o.Settings = settingsOr(o.Settings)
	return &Dashboard{pools: pools, evs: evs, io: io, arc: arc, opts: o, hosts: hostsPanel{lister: o.Hosts}}
}

// Name implements ui.Screen.
func (d *Dashboard) Name() string { return "Dashboard" }

// Number implements ui.Numbered.
func (d *Dashboard) Number() int { return dashboardView }

// Framed implements ui.Framed: each panel has its own frame.
func (d *Dashboard) Framed() bool { return true }

// Nested implements ui.Screen.
func (d *Dashboard) Nested() bool { return false }

// Init implements ui.Screen: the views it shows load themselves.
func (d *Dashboard) Init() tea.Cmd { return nil }

// Keys implements ui.Screen. Any key also marks new problems as seen.
func (d *Dashboard) Keys() []key.Binding {
	bs := []key.Binding{keys.As(keys.Right, "next panel"), keys.As(keys.Left, "previous panel")}
	switch d.focused() {
	case "health", "io", "events":
		bs = append(bs, keys.As(keys.Select, "open"), keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"))
	}
	return bs
}

func (d *Dashboard) focused() string {
	if d.focus < len(d.opts.Settings.Dashboard.Panels) {
		return d.opts.Settings.Dashboard.Panels[d.focus]
	}
	return ""
}

// Activate implements ui.Activator: the views it shows read at their
// shown rates, and zpool iostat starts.
func (d *Dashboard) Activate() tea.Cmd {
	cmds := []tea.Cmd{d.io.Activate(), d.arc.Activate(), d.evs.Activate()}
	if slices.Contains(d.opts.Settings.Dashboard.Panels, "hosts") {
		cmds = append(cmds, d.hosts.activate(*d.opts.Settings))
	}
	return tea.Batch(cmds...)
}

// Deactivate implements ui.Deactivator.
func (d *Dashboard) Deactivate() tea.Cmd {
	d.hosts.deactivate()
	return tea.Batch(d.arc.Deactivate(), d.evs.Deactivate()) // I/O keeps recording
}

// Status implements ui.Screen: the pools' age and summary.
func (d *Dashboard) Status() ui.Status { return d.pools.Status() }

// Update implements ui.Screen. The views it shows receive their own
// messages from the root; a key press here only marks problems seen.
func (d *Dashboard) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	if cmd, ok := d.hosts.update(msg, *d.opts.Settings); ok {
		return d, cmd
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		d.seenErrors, d.seenWarnings = d.counts()
		d.seenProblems = len(d.pools.visibleWarnings())
		return d, d.key(msg)
	case ui.ScrollMsg:
		d.move(msg.Delta)
	case ui.ClickMsg:
		d.click(msg)
	}
	return d, nil
}

func (d *Dashboard) key(msg tea.KeyPressMsg) tea.Cmd {
	n := len(d.opts.Settings.Dashboard.Panels)
	switch {
	case key.Matches(msg, keys.As(keys.Right, "next panel")):
		d.focus = (d.focus + 1) % max(n, 1)
	case key.Matches(msg, keys.As(keys.Left, "previous panel")):
		d.focus = (d.focus + n - 1) % max(n, 1)
	case key.Matches(msg, keys.As(keys.Down, "next")):
		d.move(1)
	case key.Matches(msg, keys.As(keys.Up, "previous")):
		d.move(-1)
	case key.Matches(msg, keys.As(keys.Select, "open")):
		return d.open()
	}
	return nil
}

// move moves the selection in the focused panel.
func (d *Dashboard) move(delta int) {
	switch d.focused() {
	case "health":
		d.pools.cursor = max(min(d.pools.cursor+delta, len(d.pools.poll.data)-1), 0)
	case "io":
		d.io.cursor.i += delta
		d.io.cursor.clamp(len(d.io.latest))
	case "events":
		d.evCursor = max(min(d.evCursor+delta, len(d.evShown)-1), 0)
	}
}

// open shows what is selected in its own view: the pool's detail, the
// pool in I/O, or the event's detail in Events.
func (d *Dashboard) open() tea.Cmd {
	view := 0
	switch d.focused() {
	case "health":
		if len(d.pools.poll.data) == 0 {
			return nil
		}
		view = poolsView
		cmd := d.pools.open()
		return tea.Batch(cmd, switchView(view))
	case "io":
		view = ioView
	case "events":
		if d.evCursor >= len(d.evShown) {
			return nil
		}
		d.evs.showEvent(d.evShown[d.evCursor])
		view = eventsView
	default:
		return nil
	}
	return switchView(view)
}

func switchView(n int) tea.Cmd { return func() tea.Msg { return ui.SwitchViewMsg{View: n} } }

// click focuses the panel under the pointer and selects the row clicked.
func (d *Dashboard) click(msg ui.ClickMsg) {
	for i, r := range d.rects {
		if msg.X < r.x || msg.X >= r.x+r.w || msg.Y < r.y || msg.Y >= r.y+r.h {
			continue
		}
		d.focus = i
		row := msg.Y - r.y - 1 // below the top edge
		switch r.name {
		case "health":
			if p := d.pools.first + row - 1; row >= 1 && p < len(d.pools.poll.data) { // below the column titles
				d.pools.cursor = p
			}
		case "io":
			if p := d.io.cursor.first + row - 1; row >= 1 && p < len(d.io.latest) {
				d.io.cursor.i = p
			}
		case "events":
			if e := d.evFirst + row; row >= 0 && e < len(d.evShown) {
				d.evCursor = e
			}
		}
		return
	}
}

// counts are the kept events that are errors and warnings.
func (d *Dashboard) counts() (errs, warns int) {
	for _, e := range d.evs.log.Events() {
		switch e.Severity {
		case events.Error:
			errs++
		case events.Warn:
			warns++
		case events.Info, events.Notice:
		}
	}
	return errs, warns
}

// Render implements ui.Screen: the panels in columns, in the configured
// order, read across.
func (d *Dashboard) Render(disp ui.Display, width, height int) string {
	cols := d.opts.Settings.Dashboard.Columns
	if cols == 0 {
		cols = 1
		if width >= d.opts.Settings.Dashboard.TwoColumnsFrom {
			cols = 2
		}
		if width >= d.opts.Settings.Dashboard.ThreeColumnsFrom {
			cols = 3
		}
	}
	panels := d.opts.Settings.Dashboard.Panels
	cols = max(min(cols, len(panels)), 1)
	d.rects = make([]panelBox, len(panels))
	d.focus = min(d.focus, max(len(panels)-1, 0))
	stacks := make([][]string, cols)
	for i, name := range panels {
		stacks[i%cols] = append(stacks[i%cols], name)
	}
	parts := make([]string, cols)
	for c, stack := range stacks {
		w := width / cols
		if c == cols-1 {
			w = width - w*(cols-1)
		}
		boxes := make([]string, len(stack))
		heights := d.stackHeights(stack, height)
		y := 0
		for i, name := range stack {
			h := heights[i]
			idx := c + i*cols // the panel's place in dashboard.panels
			d.rects[idx] = panelBox{name: name, x: (width / cols) * c, y: y, w: w, h: h}
			boxes[i] = d.panel(disp, name, idx == d.focus, w, h)
			y += h
		}
		parts[c] = lipgloss.JoinVertical(lipgloss.Left, boxes...)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// stackHeights shares a column's height among its panels: evenly, except
// that the health panel gets the rows its pools and warnings need, up to
// two fifths of the column, so a short terminal still lists the pools.
func (d *Dashboard) stackHeights(stack []string, height int) []int {
	hs := make([]int, len(stack))
	rest, n := height, len(stack)
	for i, name := range stack {
		if name == "health" && n > 1 {
			need := 3 + len(d.pools.poll.data) + len(d.pools.visibleWarnings()) // borders, column titles
			hs[i] = max(min(need, height*2/5), height/n)
			rest, n = rest-hs[i], n-1
		}
	}
	k := 0
	for i := range hs {
		if hs[i] == 0 {
			hs[i] = rest / n
			if k == n-1 {
				hs[i] = rest - hs[i]*(n-1)
			}
			k++
		}
	}
	return hs
}

func (d *Dashboard) panel(disp ui.Display, name string, focused bool, width, height int) string {
	iw, ih := ui.Inner(width, height)
	title, body := name, ""
	switch name {
	case "health":
		body = d.pools.list(disp, iw, ih)
		if n := len(d.pools.visibleWarnings()) - d.seenProblems; n > 0 {
			title = fmt.Sprintf("health: %d new %s", n, format.Plural(n, "warning", "warnings"))
		}
	case "events":
		title, body = d.eventsPanel(disp, iw, ih)
	case "io":
		body = d.ioPanel(disp, iw, ih)
	case "arc":
		body = d.arcPanel(disp, iw, ih)
	case "scans":
		body = d.scansPanel(disp)
	case "hosts":
		body = d.hosts.render(disp, iw)
	}
	return ui.Panel{Title: title, Focused: focused}.Render(disp, body, width, height)
}

// eventsPanel lists the newest warnings and errors.
func (d *Dashboard) eventsPanel(disp ui.Display, width, height int) (title, body string) {
	muted := disp.Palette.Style(theme.Muted)
	title = "events"
	if !d.evs.opts.Root {
		return title, muted.Render("ZFS events need root.")
	}
	errs, warns := d.counts()
	switch {
	case errs > d.seenErrors:
		title = fmt.Sprintf("events: %d new %s", errs-d.seenErrors, format.Plural(errs-d.seenErrors, "error", "errors"))
	case warns > d.seenWarnings:
		title = fmt.Sprintf("events: %d new %s", warns-d.seenWarnings, format.Plural(warns-d.seenWarnings, "warning", "warnings"))
	}
	all := d.evs.log.Events()
	c := d.evs.columns(width)
	now := d.opts.Now()
	d.evShown = d.evShown[:0]
	var shown []events.Event
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Severity >= events.Warn {
			shown = append(shown, all[i])
			d.evShown = append(d.evShown, all[i].ID)
		}
	}
	d.evCursor = max(min(d.evCursor, len(shown)-1), 0)
	d.evFirst = max(0, d.evCursor-height+1)
	var lines []string
	for i := d.evFirst; i < len(shown) && len(lines) < height; i++ {
		lines = append(lines, c.row(disp, shown[i], i == d.evCursor && d.focused() == "events", now))
	}
	if len(lines) == 0 {
		return title, muted.Render("No warnings or errors among the kept events.")
	}
	return title, strings.Join(lines, "\n")
}

func (d *Dashboard) ioPanel(disp ui.Display, width, height int) string {
	if d.io.at.IsZero() {
		return disp.Palette.Style(theme.Muted).Render("Waiting for zpool iostat")
	}
	lines := d.io.poolLines(disp, width, min(len(d.io.latest)+1, height))
	if charts := d.io.chartLines(disp, width, height-len(lines)-1); len(charts) > 0 {
		lines = append(append(lines, ""), charts...)
	}
	return strings.Join(lines, "\n")
}

// arcPanel is the ARC's size against its maximum, as a gauge and a
// graph of the last five minutes, and its hit ratio.
func (d *Dashboard) arcPanel(disp ui.Display, width, height int) string {
	a := d.arc
	muted := disp.Palette.Style(theme.Muted)
	if a.poll.loaded.IsZero() {
		return muted.Render("Reading ARC statistics")
	}
	s := a.poll.data
	sizes := format.Size(s.Size, disp.Exact) + muted.Render(" of ") + format.Size(s.Max, disp.Exact)
	gauge := max(width-lipgloss.Width(sizes)-gap, 1)
	now := muted.Render("no reads")
	if v, ok := a.hits.last(); ok {
		now = fmt.Sprintf("%.1f%%", v)
	}
	lines := []string{
		ui.Gauge(disp, pctOf(s.Size, s.Max), gauge) + sp + sizes,
		muted.Render("hit ratio now ") + now + muted.Render("   since load ") + fmt.Sprintf("%.1f%%", sinceLoad(s)),
	}
	if rows := height - len(lines) - 1; rows > 0 {
		graphW := max(width-axisWidth, 1)
		lines = append(lines, muted.Render("size, last 5 minutes"))
		lines = append(lines, axisGraph(disp, a.sizes.slots(a.poll.loaded, a.set.Charts.ARCWindow, ui.GraphSamples(disp, graphW)), "max  ", graphW, rows)...)
	}
	return strings.Join(lines, "\n")
}

// scansPanel lists running scrubs, resilvers, RAIDZ expansions and vdev
// removals with their progress.
func (d *Dashboard) scansPanel(disp ui.Display) string {
	var lines []string
	now := d.opts.Now()
	for _, o := range d.pools.poll.data {
		if sc := o.Status.Scan; sc != nil && sc.State == "SCANNING" {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render(o.Pool.Name.String())+sp+scanLine(*sc, now, disp.Exact))
		}
		if o.Status.Expanding() {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render(o.Pool.Name.String())+sp+expandLine(o.Status, now, disp.Exact))
		}
		if o.Status.Removing() {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render(o.Pool.Name.String())+sp+removeLine(o.Status, now, disp.Exact))
		}
	}
	if len(lines) == 0 {
		return disp.Palette.Style(theme.Muted).Render("Nothing running: no scrub, resilver, expansion or removal.")
	}
	return strings.Join(lines, "\n")
}

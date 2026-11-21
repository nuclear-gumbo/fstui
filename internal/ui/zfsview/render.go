// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"fmt"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/problem"

	"github.com/nuclear-gumbo/fstui/internal/zfs/health"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"github.com/nuclear-gumbo/fstui/internal/ui/keys"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// Column widths in cells.
const (
	gap         = 2
	markerWidth = 2 // "> " on the selected row
	healthWidth = len("DEGRADED")
	pctWidth    = len("100%")
	levelWidth  = len("WARN")
	sizeWidth   = len("1023 MiB")
	errorsWidth = len("ERRORS")
	scrubWidth  = len("resilver 100%")
	fragWidth   = len("FRAG")
	dedupWidth  = len("DEDUP")
	maxName     = 24
)

// columns says which optional columns fit and how wide the bar is.
type columns struct {
	name               int
	bar                int // 0: no bar
	space, errors      bool
	scrub, frag, dedup bool
}

// layout fits the optional columns into width in priority order: used
// and available, device errors, the bar at its minimum, scrub,
// fragmentation, dedup; then the bar grows. Name, health and the
// percentage are always shown, so the list also fits a side panel.
func layout(pools []zfs.Overview, width int) columns {
	c := columns{name: len("NAME")}
	for _, o := range pools {
		c.name = max(c.name, len(o.Pool.Name.String()))
	}
	c.name = min(c.name, maxName)
	spare := width - (markerWidth + c.name + gap + healthWidth + gap + pctWidth + 1 + levelWidth)
	take := func(on *bool, w int) {
		if spare >= w {
			*on, spare = true, spare-w
		}
	}
	take(&c.space, 2*(gap+sizeWidth))
	take(&c.errors, gap+errorsWidth)
	if spare >= ui.BarMinWidth+gap {
		c.bar, spare = ui.BarMinWidth, spare-ui.BarMinWidth-gap
	}
	take(&c.scrub, gap+scrubWidth)
	take(&c.frag, gap+fragWidth)
	take(&c.dedup, gap+dedupWidth)
	if c.bar > 0 {
		c.bar += min(spare, ui.BarWidth-ui.BarMinWidth)
	}
	return c
}

// List panel width in the split layout: two fifths, within these bounds.
const (
	minListPanel = 44
	maxListPanel = 72
)

// Render implements ui.Screen.
func (p *Pools) Render(d ui.Display, width, height int) string {
	split := width >= p.opts.Settings.UI.SplitFrom && len(p.poll.data) > 0
	if split && p.detail == nil {
		p.follow() // the first render picks the selected pool; Topology needs no read
	}
	var out string
	out, p.geo = splitPanels(d, width, height, split, p.focus && p.detail != nil, p.listPanel, p.detailPanel)
	return out
}

// panelFunc draws one panel of a view at the given size.
type panelFunc func(d ui.Display, width, height int, focused bool) string

// splitPanels lays out a list and its detail: side by side when split,
// otherwise whichever has the focus fills the view. It returns where the
// panels went, for the mouse.
func splitPanels(d ui.Display, width, height int, split, inDetail bool, list, detail panelFunc) (string, geometry) {
	if split {
		lw := min(max(width*2/5, minListPanel), maxListPanel)
		return lipgloss.JoinHorizontal(lipgloss.Top,
				list(d, lw, height, !inDetail), detail(d, width-lw, height, inDetail)),
			geometry{split: true, left: lw, width: width}
	}
	if inDetail {
		return detail(d, width, height, true), geometry{width: width}
	}
	return list(d, width, height, true), geometry{width: width}
}

func (p *Pools) listPanel(d ui.Display, width, height int, focused bool) string {
	iw, ih := ui.Inner(width, height)
	return ui.Panel{Title: "pools", Focused: focused}.Render(d, p.list(d, iw, ih), width, height)
}

// list draws the pool table and the warnings under it.
func (p *Pools) list(d ui.Display, width, height int) string {
	muted := d.Palette.Style(theme.Muted)
	switch {
	case len(p.poll.data) == 0 && p.poll.loaded.IsZero() && p.poll.lastErr.IsEmpty():
		return muted.Render("Reading pools")
	case len(p.poll.data) == 0 && p.poll.loaded.IsZero():
		return "Could not read pools\n" + p.poll.lastErr.Display() +
			"\n" + muted.Render("fstui retries automatically; r retries now.")
	case len(p.poll.data) == 0:
		act, w := keys.Get(keys.Actions).Help().Key, keys.Get(keys.WriteMode).Help().Key
		return "No pools are imported.\n" + muted.Render(fmt.Sprintf("In write mode (%s), %s then I imports a pool, %s then N creates one.", w, act, act))
	}
	c := layout(p.poll.data, width)
	block := warningBlock(d, p.visibleWarnings(), width, height)
	wl := len(block)
	// Rows for the table: what is left after the header and the warnings,
	// keeping at least one pool row.
	rows := max(height-1-wl, 1)
	start := max(0, p.cursor-rows+1)
	p.first = start
	lines := []string{muted.Render(header(c))}
	for i := start; i < len(p.poll.data) && i < start+rows; i++ {
		lines = append(lines, p.row(d, c, p.poll.data[i], i == p.cursor))
	}
	for len(lines) < height-wl {
		lines = append(lines, "")
	}
	return strings.Join(append(lines, block...), "\n")
}

func header(c columns) string {
	h := strings.Repeat(" ", markerWidth) + pad("NAME", c.name) + sp + pad("HEALTH", healthWidth) + sp
	if c.bar > 0 {
		h += pad("CAPACITY", c.bar) + sp
	}
	h += lpad("USE", pctWidth) + " " + strings.Repeat(" ", levelWidth)
	if c.space {
		h += sp + lpad("USED", sizeWidth) + sp + lpad("AVAIL", sizeWidth)
	}
	if c.frag {
		h += sp + lpad("FRAG", fragWidth)
	}
	if c.dedup {
		h += sp + lpad("DEDUP", dedupWidth)
	}
	if c.scrub {
		h += sp + pad("SCRUB", scrubWidth)
	}
	if c.errors {
		h += sp + lpad("ERRORS", errorsWidth)
	}
	return h
}

var sp = strings.Repeat(" ", gap)

func (p *Pools) row(d ui.Display, c columns, o zfs.Overview, selected bool) string {
	pal := d.Palette
	th := p.opts.Thresholds
	pct := health.CapacityPct(o)
	level := ui.LevelOf(pct, th.CapacityWarn, th.CapacityError)
	marker, name := "  ", pad(truncName(o.Pool.Name.String(), c.name), c.name)
	if selected {
		marker = pal.Style(theme.Accent).Render(">") + " "
		name = lipgloss.NewStyle().Bold(true).Render(name)
	}
	r := marker + name + sp + pal.Style(stateToken(o.Status.State)).Render(pad(o.Status.State, healthWidth)) + sp
	if c.bar > 0 {
		r += ui.Bar(d, pct, c.bar, th.CapacityWarn, th.CapacityError) + sp
	}
	levelTok := map[ui.Level]theme.Token{ui.Warning: theme.Warn, ui.Critical: theme.Error}[level]
	r += lpad(fmt.Sprintf("%d%%", pct), pctWidth) + " " + pal.Style(levelTok).Render(pad(ui.LevelWord(level), levelWidth))
	if c.space {
		r += sp + lpad(format.Size(o.Used, d.Exact), sizeWidth) + sp + lpad(format.Size(o.Available, d.Exact), sizeWidth)
	}
	if c.frag {
		r += sp + lpad(fragText(o), fragWidth)
	}
	if c.dedup {
		r += sp + lpad(o.Pool.Properties["dedupratio"].Text.Display()+"x", dedupWidth)
	}
	if c.scrub {
		r += sp + pad(scrubText(o, p.opts.Now), scrubWidth)
	}
	if !c.errors {
		return r
	}
	errs := deviceErrors(o.Status)
	errTok := theme.Muted
	if errs > 0 {
		errTok = theme.Error
	}
	return r + sp + pal.Style(errTok).Render(lpad(fmt.Sprint(errs), errorsWidth))
}

func pad(s string, w int) string  { return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s))) }
func lpad(s string, w int) string { return strings.Repeat(" ", max(0, w-lipgloss.Width(s))) + s }

func truncName(s string, w int) string {
	if len(s) <= w {
		return s
	}
	return s[:w-1] + "…"
}

// stateToken colours a state word; the word itself always carries the meaning.
func stateToken(state string) theme.Token {
	switch state {
	case "ONLINE", "AVAIL", "INUSE":
		return theme.OK
	case "DEGRADED":
		return theme.Warn
	case "OFFLINE":
		return theme.Muted
	}
	return theme.Error
}

func fragText(o zfs.Overview) string {
	if f, ok := o.Pool.Properties.Number("fragmentation"); ok {
		return fmt.Sprintf("%d%%", f)
	}
	return "-"
}

func scrubText(o zfs.Overview, now func() time.Time) string {
	word, state := scanParts(o, now)
	if word == "scrub" && !strings.Contains(state, "%") {
		return state // the column is SCRUB: "3h ago", "never"
	}
	return word + " " + state
}

// scanParts names the pool's last scan, which zpool status keeps whether
// it was a scrub or a resilver, and says where it stands: "36%", "paused
// 36%", "cancelled", "3h ago", or "never" with the word "scrub".
func scanParts(o zfs.Overview, now func() time.Time) (word, state string) {
	sc := o.Status.Scan
	if sc == nil {
		return "scrub", "never"
	}
	word = strings.ToLower(sc.Function)
	pct := 0
	if sc.ToExamine > 0 {
		pct = int(sc.Issued * 100 / sc.ToExamine) //nolint:gosec // a percentage
	}
	switch {
	case sc.State == "SCANNING" && sc.Paused:
		return word, fmt.Sprintf("paused %d%%", pct)
	case sc.State == "SCANNING":
		return word, fmt.Sprintf("%d%%", pct)
	case sc.State == "CANCELED":
		return word, "cancelled"
	}
	state = format.Age(now().Sub(sc.End)) + " ago"
	if sc.Errors > 0 {
		state += fmt.Sprintf(", %d err", sc.Errors)
	}
	return word, state
}

// deviceErrors sums read, write and checksum errors over every device.
func deviceErrors(st zfs.PoolStatus) uint64 {
	var sum uint64
	st.Walk(func(v zfs.Vdev) { sum += v.ReadErrors + v.WriteErrors + v.ChecksumErrors })
	return sum
}

// warningBlock draws the warnings under the pool list: a blank line,
// then each warning wrapped to width with its continuation lines
// indented, as many whole warnings as fit in a third of the height (and
// always the first), and "+N more" for the rest. A warning is never cut
// short: "1 device O…" at the edge of a narrow list says nothing.
func warningBlock(d ui.Display, ws []problem.Problem, width, height int) []string {
	if len(ws) == 0 {
		return nil
	}
	budget := max(height/3, 1) // a third of the view
	out := []string{""}
	for i, w := range ws {
		lines := wrapIndented(w.Text, width, warnIndent)
		more := 0
		if i < len(ws)-1 {
			more = 1 // room for "+N more" if a later one does not fit
		}
		if i > 0 && len(out)-1+len(lines)+more > budget {
			return append(out, d.Palette.Style(theme.Muted).Render(fmt.Sprintf("+%d more", len(ws)-i)))
		}
		tok := theme.Warn
		if !w.Advisory {
			tok = theme.Error
		}
		for _, l := range lines {
			out = append(out, d.Palette.Style(tok).Render(l))
		}
	}
	return out
}

// warnIndent indents a warning's continuation lines.
const warnIndent = 2

// wrapIndented wraps s to width, indenting every line after the first.
func wrapIndented(s string, width, indent int) []string {
	first := strings.SplitN(ansi.Wrap(s, max(width, 1), ""), "\n", 2)
	out := []string{first[0]}
	if len(first) == 1 {
		return out
	}
	rest := strings.TrimSpace(s[len(first[0]):])
	for _, l := range strings.Split(ansi.Wrap(rest, max(width-indent, 1), ""), "\n") {
		out = append(out, strings.Repeat(" ", indent)+l)
	}
	return out
}

// figure is the status bar's view figure, e.g. "2 pools, 1 DEGRADED".
func (p *Pools) figure() string {
	if p.poll.loaded.IsZero() {
		return ""
	}
	s := fmt.Sprintf("%d %s", len(p.poll.data), format.Plural(len(p.poll.data), "pool", "pools"))
	bad := map[string]int{}
	for _, o := range p.poll.data {
		if o.Status.State != "ONLINE" {
			bad[o.Status.State]++
		}
	}
	for _, st := range []string{"DEGRADED", "FAULTED", "UNAVAIL", "SUSPENDED", "OFFLINE", "REMOVED"} {
		if bad[st] > 0 {
			s += fmt.Sprintf(", %d %s", bad[st], st)
		}
	}
	if n := p.hiddenCount(); n > 0 {
		s += fmt.Sprintf(", %d %s hidden", n, format.Plural(n, "warning", "warnings"))
	}
	return s
}

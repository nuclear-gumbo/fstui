// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// eventsListTop is the screen row of the first event: the panel edge,
// the header line and the column titles.
const eventsListTop = 3

const (
	sevWidth    = len("NOTICE")
	clockWidth  = len("10:33:03")
	dateWidth   = len("Nov 14 10:33")
	poolMax     = 20
	summaryMin  = 20 // the summary keeps this much before the device column gets any
	deviceShare = 4  // the device column takes at most a quarter of the width
)

// Framed implements ui.Framed: list and detail are panels of their own.
func (v *Events) Framed() bool { return true }

// Render implements ui.Screen.
func (v *Events) Render(d ui.Display, width, height int) string {
	shown := v.shown()
	split := width >= splitWidth && len(shown) > 0
	var out string
	out, v.geo = splitPanels(d, width, height, split, v.focus && len(shown) > 0, v.listPanel, v.detailPanel)
	return out
}

func (v *Events) listPanel(d ui.Display, width, height int, focused bool) string {
	iw, ih := ui.Inner(width, height)
	return ui.Panel{Title: "events", Focused: focused}.Render(d, v.list(d, iw, ih), width, height)
}

func (v *Events) list(d ui.Display, width, height int) string {
	muted := d.Palette.Style(theme.Muted)
	switch {
	case !v.opts.Root:
		return "ZFS events need root: zpool events refuses other users.\n" +
			muted.Render("Run fstui as root to see them.")
	case v.poll.loaded.IsZero() && !v.poll.lastErr.IsEmpty():
		return "Could not read events\n" + v.poll.lastErr.Display()
	case v.poll.loaded.IsZero():
		return muted.Render("Reading events")
	}
	shown := v.shown()
	c := v.columns(width)
	lines := []string{v.header(d, len(shown)), muted.Render(c.title())}
	rows := max(height-len(lines)-1, 1) // a line for the text filter
	start := max(0, v.cursor-rows+1)
	v.first = start
	now := v.opts.Now()
	for i := start; i < len(shown) && i < start+rows; i++ {
		lines = append(lines, c.row(d, shown[i], i == v.cursor, now))
	}
	if len(shown) == 0 {
		lines = append(lines, muted.Render("No events match."))
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	if v.input != nil {
		lines = append(lines, d.Palette.Style(theme.Accent).Render("/ ")+*v.input+ui.Cursor(d))
	}
	return strings.Join(lines, "\n")
}

// header says how many events are kept and shown, the filter, and
// whether the list follows the newest event.
func (v *Events) header(d ui.Display, shown int) string {
	muted := d.Palette.Style(theme.Muted)
	h := fmt.Sprintf("%d kept, %d shown", len(v.log.Events()), shown) + muted.Render("   "+v.filter.describe()+"   ")
	switch {
	case v.follow:
		h += muted.Render("following")
	case v.unseen > 0:
		h += d.Palette.Style(theme.Accent).Render(fmt.Sprintf("paused, %d new", v.unseen))
	default:
		h += muted.Render("paused")
	}
	if n := v.log.Dropped(); n > 0 {
		h += d.Palette.Style(theme.Warn).Render(fmt.Sprintf("   the kernel dropped %d", n))
	}
	return h
}

type eventColumns struct{ time, pool, device, summary int }

// columns sizes the list: time, severity and pool always; the summary
// before the device, which the detail always shows.
func (v *Events) columns(width int) eventColumns {
	c := eventColumns{time: clockWidth, pool: len("POOL")}
	now := v.opts.Now()
	for _, e := range v.log.Events() {
		c.pool = max(c.pool, lipgloss.Width(e.Source.Display()))
		if len(eventTime(e.Time, now)) > clockWidth {
			c.time = dateWidth
		}
	}
	c.pool = min(c.pool, poolMax, max(width/5, len("POOL"))) //nolint:mnd // a fifth of a narrow list
	fixed := markerWidth + c.time + sevWidth + c.pool + 3*gap
	if spare := width - fixed - summaryMin - gap; spare > len("DEVICE") {
		c.device = min(spare, width/deviceShare)
	}
	c.summary = max(width-fixed-c.device, 1)
	if c.device > 0 {
		c.summary -= gap
	}
	return c
}

func (c eventColumns) title() string {
	t := strings.Repeat(" ", markerWidth) + pad("TIME", c.time) + sp + pad("SEV", sevWidth) + sp + pad("POOL", c.pool) + sp
	if c.device > 0 {
		t += pad("DEVICE", c.device) + sp
	}
	return t + "SUMMARY"
}

func (c eventColumns) row(d ui.Display, e events.Event, selected bool, now time.Time) string {
	marker := "  "
	summary := ansi.Truncate(e.Summary.Display(), c.summary, "…")
	if selected {
		marker = d.Palette.Style(theme.Accent).Render(">") + " "
		summary = lipgloss.NewStyle().Bold(true).Render(summary)
	}
	r := marker + pad(eventTime(e.Time, now), c.time) + sp + d.Palette.Style(severityToken(e.Severity)).Render(pad(e.Severity.String(), sevWidth)) +
		sp + pad(ansi.Truncate(e.Source.Display(), c.pool, "…"), c.pool) + sp
	if c.device > 0 {
		r += d.Palette.Style(theme.Muted).Render(pad(leftTrunc(e.Device.Display(), c.device), c.device)) + sp
	}
	return r + summary
}

// eventTime is the time of day for today's events, otherwise the date.
func eventTime(t, now time.Time) string {
	t = t.In(now.Location())
	if y, m, dd := t.Date(); y == now.Year() && m == now.Month() && dd == now.Day() {
		return t.Format("15:04:05")
	}
	return t.Format("Jan 02 15:04")
}

// severityToken colours the severity word: the word carries the meaning,
// the colour draws the eye.
func severityToken(s events.Severity) theme.Token {
	switch s {
	case events.Error:
		return theme.Error
	case events.Warn:
		return theme.Warn
	case events.Info:
		return theme.Muted
	case events.Notice:
	}
	return theme.Accent
}

// detailPanel shows every field of the selected event.
func (v *Events) detailPanel(d ui.Display, width, height int, focused bool) string {
	iw, ih := ui.Inner(width, height)
	shown := v.shown()
	if v.cursor >= len(shown) {
		return ui.Panel{Title: "event", Focused: focused}.Render(d, "", width, height)
	}
	e := shown[v.cursor]
	muted := d.Palette.Style(theme.Muted)
	nameCol := len("severity")
	for _, f := range e.Detail {
		nameCol = max(nameCol, len(f.Name))
	}
	nameCol = min(nameCol, iw/2) //nolint:mnd // names never take more than half
	line := func(name, value string) string {
		return muted.Render(pad(name, nameCol)) + sp + ansi.Truncate(value, max(iw-nameCol-gap, 1), "…")
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(e.Summary.Display()),
		line("time", e.Time.In(v.opts.Now().Location()).Format("2006-01-02 15:04:05.000")),
		line("severity", d.Palette.Style(severityToken(e.Severity)).Render(e.Severity.String())),
		"",
	}
	for _, f := range e.Detail {
		lines = append(lines, line(f.Name, f.Value.Display()))
	}
	title := ansi.Truncate(e.Class.Display(), max(iw-4, 1), "…")
	return ui.Panel{Title: title, Focused: focused}.Render(d, strings.Join(scrollTo(lines, ih), "\n"), width, height)
}

// scrollTo keeps the first height lines.
func scrollTo(lines []string, height int) []string { return lines[:min(len(lines), max(height, 0))] }

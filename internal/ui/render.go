// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// fieldGap separates status bar fields and key hints.
const fieldGap = "  "

// maxHints is the most keys the hint bar shows.
const maxHints = 6

// ellipsis marks truncated text.
const ellipsis = "…"

func (m Model) render() string {
	if m.width < MinWidth || m.height < MinHeight {
		return sizeNotice(m.width, m.height)
	}
	notice := m.noticeLines()
	body := m.height - frameLines - len(notice)
	var content string
	var bindings []key.Binding
	var st Status
	d := m.display
	if len(m.screens) > 0 {
		s := m.screens[m.active]
		bindings, st = append(m.changeHints(), s.Keys()...), s.Status()
		if framed(s) {
			content = s.Render(d, m.width, body)
		} else {
			iw, ih := Inner(m.width, body)
			content = Panel{Title: strings.ToLower(s.Name()), Focused: true}.Render(d, s.Render(d, iw, ih), m.width, body)
		}
	}
	if m.help {
		content = Panel{Title: "keys", Focused: true}.Render(d, m.helpText(bindings), m.width, body)
	}
	if m.modal != nil {
		content = overlay(block(content, m.width, body), m.modal.Render(d, m.width-4, body-2), m.width, body)
		bindings = m.modal.Keys()
	}
	lines := []string{fit(m.viewBar(d), m.width), block(content, m.width, body)}
	for _, n := range notice {
		lines = append(lines, fit(n, m.width))
	}
	// Key hints at the bottom left, the session's status at the bottom
	// right, so the two read as separate.
	// The status line reads from the left, like the hints.
	lines = append(lines, fit(m.hintBar(bindings), m.width), fit(m.statusBar(st), m.width))
	out := strings.Join(lines, "\n")
	if m.display.ASCII {
		// Views truncate with "…"; the Linux console's fonts may lack it,
		// so ASCII mode shows "~", wherever it came from.
		out = strings.ReplaceAll(out, "…", "~")
	}
	return out
}

// viewBar lists the views by number, the active one in bold accent, so
// every view can be found without the help. A click on a name opens it.
func (m Model) viewBar(d Display) string {
	labels, sep := m.viewLabels(d)
	parts := make([]string, len(labels))
	for i, label := range labels {
		st := d.Palette.Style(theme.Muted)
		if i == m.active {
			st = d.Palette.Style(theme.Accent).Bold(true)
		}
		parts[i] = st.Render(label)
	}
	return " " + strings.Join(parts, sep)
}

// shortName is how long view names get when even the abbreviations do
// not fit (ASCII labels such as "9:settings" are longer).
const shortName = 4

// abbreviations shorten the longest view names when the bar would not
// fit 80 columns otherwise; the rest stay whole.
var abbreviations = map[string]string{"dashboard": "dash", "datasets": "data", "snapshots": "snaps", "volumes": "vols"}

// viewLabels are the view bar's labels and the space between them: the
// full names if they fit, else the abbreviations, else names cut to four
// letters, so every view stays visible and clickable at 80 columns.
func (m Model) viewLabels(d Display) (labels []string, sep string) {
	full := make([]string, len(m.screens))
	abbr := make([]string, len(m.screens))
	short := make([]string, len(m.screens))
	for i, s := range m.screens {
		num, name := viewNumber(d, m.number(i)), strings.ToLower(s.Name())
		full[i], abbr[i], short[i] = num+name, num+cmp.Or(abbreviations[name], name), num+ansi.Truncate(name, shortName, "")
	}
	width := func(ls []string, sep string) int {
		w := 1 + len(sep)*(len(ls)-1)
		for _, l := range ls {
			w += lipgloss.Width(l)
		}
		return w
	}
	switch {
	case width(full, fieldGap) <= m.width:
		return full, fieldGap
	case width(full, " ") <= m.width:
		return full, " "
	case width(abbr, fieldGap) <= m.width:
		return abbr, fieldGap
	case width(abbr, " ") <= m.width:
		return abbr, " "
	}
	return short, " "
}

// viewAt returns the screen whose name in the view bar is under column
// x, or -1.
func (m Model) viewAt(x int) int {
	pos := 1
	labels, sep := m.viewLabels(m.display)
	for i, l := range labels {
		w := lipgloss.Width(l)
		if x >= pos && x < pos+w {
			return i
		}
		pos += w + len(sep)
	}
	return -1
}

// noticeLines shows the first notice and how many more there are, in two
// lines at most.
func (m Model) noticeLines() []string {
	n := m.session.Notices
	if len(n) == 0 {
		return nil
	}
	warn, muted := m.display.Palette.Style(theme.Warn), m.display.Palette.Style(theme.Muted)
	second := "esc dismisses"
	if len(n) > 1 {
		second = fmt.Sprintf("and %d more; esc dismisses", len(n)-1)
	}
	return []string{warn.Render(n[0]), muted.Render(second)}
}

// sizeNotice replaces everything below the minimum size.
func sizeNotice(w, h int) string {
	msg := fmt.Sprintf("Terminal is %dx%d. fstui needs at least %dx%d.\nResize the window, or press q to quit.",
		w, h, MinWidth, MinHeight)
	if w <= 0 || h <= 0 {
		return ""
	}
	// Wrapped to the width and cut to the height: a terminal wraps a line
	// longer than itself, which would scroll the notice off a tiny screen.
	lines := strings.Split(ansi.Wrap(msg, w, " "), "\n")
	lines = lines[:min(len(lines), h)]
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "")
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

// fit cuts or pads one line to exactly width cells.
func fit(s string, width int) string {
	s = ansi.Truncate(s, width, ellipsis)
	if pad := width - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// block makes s exactly height lines of width cells, so a screen that
// draws too much or too little cannot move the bars.
func block(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if s == "" {
		lines = nil
	}
	out := make([]string, height)
	for i := range out {
		var l string
		if i < len(lines) {
			l = lines[i]
		}
		out[i] = fit(l, width)
	}
	return strings.Join(out, "\n")
}

func (m Model) hintBar(bindings []key.Binding) string {
	muted := m.display.Palette.Style(theme.Muted)
	keyStyle := m.display.Palette.Style(theme.Accent).Bold(true)
	h := keys.Get(keys.Help).Help()
	help := keyStyle.Render(h.Key) + " " + muted.Render(h.Desc)
	// The help key always shows: the screen's hints take what is left,
	// whole hints only, most relevant first.
	room := m.width - lipgloss.Width(help)
	var parts []string
	for _, b := range bindings {
		if len(parts) == maxHints-1 {
			break
		}
		bh := b.Help()
		if !b.Enabled() || bh.Key == "" {
			continue
		}
		part := keyStyle.Render(bh.Key) + " " + muted.Render(bh.Desc)
		if room -= lipgloss.Width(part) + len(fieldGap); room < 0 {
			break
		}
		parts = append(parts, part)
	}
	return strings.Join(append(parts, help), fieldGap)
}

// statusBar draws the status fields in order. When they do not fit, the
// view figure, then the refresh age, then the privilege are dropped; the
// mode is never dropped.
func (m Model) statusBar(st Status) string {
	muted, warn := m.display.Palette.Style(theme.Muted), m.display.Palette.Style(theme.Warn)
	host := m.session.Host.Display()
	// READ-ONLY unless the write module set another mode (ModeMsg); the
	// demo build says DEMO. A mode that can change storage stands out.
	mode := cmp.Or(m.session.Mode, "READ-ONLY")
	switch mode {
	case "WRITE":
		mode = warn.Bold(true).Render(mode)
	case "DRY RUN":
		mode = m.display.Palette.Style(theme.Accent).Bold(true).Render(mode)
	}
	privilege := m.session.Privilege
	label := m.display.Palette.Style(theme.Accent).Bold(true).Render(m.session.Label)
	if m.session.Label == "" {
		label = ""
	}
	age := muted.Render("loading")
	switch {
	case st.Static:
		age = ""
	case !st.Refreshed.IsZero():
		age = muted.Render("refreshed " + Age(m.opts.Now().Sub(st.Refreshed)) + " ago")
	}
	if line := st.LastError.FirstLine().Display(); line != "" {
		since := "never loaded"
		if !st.Refreshed.IsZero() {
			since = "stale " + Age(m.opts.Now().Sub(st.Refreshed))
		}
		age = warn.Render(since + ", last error: " + line)
	}
	figure := st.Figure
	if m.session.Superuser {
		figure = strings.TrimSpace(figure + fieldGap + muted.Render("warnings off"))
	}

	for _, fields := range [][]string{
		{host, label, mode, privilege, age, figure},
		{host, label, mode, privilege, age},
		{host, label, mode, privilege},
		{host, label, mode},
	} {
		line := strings.Join(nonEmpty(fields), fieldGap)
		if lipgloss.Width(line) <= m.width {
			return line
		}
	}
	// Even host and mode do not fit: shorten the host, keep the mode whole.
	return ansi.Truncate(host, m.width-lipgloss.Width(mode)-len(fieldGap), ellipsis) + fieldGap + mode
}

func nonEmpty(ss []string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// helpText lists the screen's keys, then the global ones, in two aligned
// columns.
func (m Model) helpText(screen []key.Binding) string {
	muted := m.display.Palette.Style(theme.Muted)
	var b strings.Builder
	section := func(title string, keys []key.Binding) {
		if len(keys) == 0 {
			return
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(title) + "\n")
		width := 0
		for _, k := range keys {
			width = max(width, lipgloss.Width(k.Help().Key))
		}
		for _, k := range keys {
			h := k.Help()
			fmt.Fprintf(&b, "  %-*s  %s\n", width, h.Key, muted.Render(h.Desc))
		}
		b.WriteString("\n")
	}
	if len(m.screens) > 0 {
		section(m.screens[m.active].Name(), screen)
	}
	if t, ok := m.selection(); ok && m.opts.Actions != nil {
		section("Changes to "+termtext.New(t.Names[0]).Display()+" (a opens the menu)", m.opts.Actions.Offered(t))
	}
	section("Everywhere", append(globalKeys(), m.writeKeys()...))
	b.WriteString(muted.Render("? or esc closes this help"))
	return b.String()
}

// Age formats a duration as fstui shows ages: 3s, 14m, 2h, 5d.
func Age(d time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d/time.Second)))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < day:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/day))
}

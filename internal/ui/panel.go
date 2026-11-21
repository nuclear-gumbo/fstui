// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// Panel is a framed section of a screen, as in btop: a rounded border with the
// title, and optionally tabs, set into its top edge. The frame takes one cell
// on each side.
type Panel struct {
	Title   string   // e.g. "pools" or a pool name
	Tabs    []string // tab names in the top edge; nil for none
	Active  int      // index of the selected tab
	Focused bool     // the panel keys go to; its border uses the accent
}

// Inner returns the content size of a panel drawn at width x height.
func Inner(width, height int) (w, h int) { return max(width-2, 0), max(height-2, 0) }

type borderSet struct{ tl, tr, bl, br, h, v, sep string }

var (
	rounded = borderSet{"╭", "╮", "╰", "╯", "─", "│", "│"}
	plain   = borderSet{"+", "+", "+", "+", "-", "|", "|"}
)

// superscript digits for view numbers in panel titles, btop style.
var superscript = []string{"⁰", "¹", "²", "³", "⁴", "⁵", "⁶", "⁷", "⁸", "⁹"}

// titleOffset is where the title starts in the top edge, after the
// corner and one line cell.
const titleOffset = 2

// Render draws the panel around content, which is cut or padded to the
// inner size.
func (p Panel) Render(d Display, content string, width, height int) string {
	if width < 2 || height < 2 {
		return ""
	}
	b := rounded
	if d.ASCII {
		b = plain
	}
	edge := d.Palette.Style(theme.Border)
	if p.Focused {
		edge = d.Palette.Style(theme.Accent)
	}
	iw, ih := Inner(width, height)
	lines := make([]string, 0, height)
	lines = append(lines, p.top(d, b, edge, width))
	body := strings.Split(content, "\n")
	for i := range ih {
		var l string
		if i < len(body) {
			l = body[i]
		}
		lines = append(lines, edge.Render(b.v)+fit(l, iw)+edge.Render(b.v))
	}
	lines = append(lines, edge.Render(b.bl+strings.Repeat(b.h, iw)+b.br))
	return strings.Join(lines, "\n")
}

// top draws the top edge: corner, title, tabs, line to the other corner.
func (p Panel) top(d Display, b borderSet, edge lipgloss.Style, width int) string {
	title := lipgloss.NewStyle().Bold(true).Render(p.label(width))
	var tabs strings.Builder
	for i, t := range p.Tabs {
		name := " " + t + " "
		if i == p.Active {
			name = d.Palette.Style(theme.Accent).Bold(true).Render(name)
		} else {
			name = d.Palette.Style(theme.Muted).Render(name)
		}
		tabs.WriteString(edge.Render(b.sep) + name)
	}
	if len(p.Tabs) > 0 {
		tabs.WriteString(edge.Render(b.sep))
	}
	head := edge.Render(b.tl+b.h) + title + tabs.String()
	head = ansi.Truncate(head, width-1, "")
	fill := width - 1 - lipgloss.Width(head)
	return head + edge.Render(strings.Repeat(b.h, max(fill, 0))+b.tr)
}

// label is the number and title, shortened so the tabs fit in a panel
// of the given width: the tabs are what the user clicks and switches
// between, while the title is also on screen elsewhere (the selected row).
func (p Panel) label(width int) string {
	room := width - titleOffset - 1 - p.tabsWidth() // the far corner
	return ansi.Truncate(p.Title, max(room, minTitle), "…")
}

// viewNumber is a view's number as titles show it: a superscript digit,
// or "1:" in ASCII mode; the 0 key's view, 10, shows as 0.
func viewNumber(d Display, n int) string {
	if n <= 0 || n > maxScreens {
		return ""
	}
	n %= len(superscript)
	if d.ASCII {
		return "0123456789"[n:n+1] + ":"
	}
	return superscript[n]
}

// minTitle keeps a few characters of a title however many tabs there are.
const minTitle = 4

// tabsWidth is the cells the tabs take: each name with a space on either
// side, a separator before each and one after the last.
func (p Panel) tabsWidth() int {
	if len(p.Tabs) == 0 {
		return 0
	}
	w := 1
	for _, t := range p.Tabs {
		w += len(t) + 3 // separator and two spaces
	}
	return w
}

// TabAt returns the tab under column x of the top edge of the panel drawn
// at width (x counts from the panel's left corner), or -1.
func (p Panel) TabAt(width, x int) int {
	pos := titleOffset + lipgloss.Width(p.label(width)) + 1 // after the separator
	for i, t := range p.Tabs {
		w := len(t) + 2 // the spaces around the name
		if x >= pos && x < pos+w {
			return i
		}
		pos += w + 1
	}
	return -1
}

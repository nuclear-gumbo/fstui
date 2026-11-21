// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// Grid is a table drawn in a frame, with a line between columns, so each
// column is a boxed section and its title sits over its cells. The last
// column takes the width left; the others are as wide as their widest
// cell. The cursor's row is marked with > and the accent.
type Grid struct {
	Cols   []string
	Rows   [][]string
	Cursor int // -1: none
	// Style colours a row other than the cursor's, such as a failure in
	// the error colour; nil or false leaves it plain.
	Style func(row int) (lipgloss.Style, bool)
}

// GridChrome is the lines a Grid adds around its rows: the top edge, the
// titles, the rule under them and the bottom edge. The first row is
// GridChrome-1 lines below the top.
const GridChrome = 4

// Render draws rows from to to (not included) in width cells.
func (g Grid) Render(d Display, width, from, to int) []string {
	b := rounded
	cross, teeT, teeB, teeL, teeR := "┼", "┬", "┴", "├", "┤"
	if d.ASCII {
		b, cross, teeT, teeB, teeL, teeR = plain, "+", "+", "+", "+", "+"
	}
	edge := d.Palette.Style(theme.Border)
	w := g.widths(width)
	rule := func(l, m, r string) string {
		parts := make([]string, len(w))
		for i, n := range w {
			parts[i] = strings.Repeat(b.h, n+2)
		}
		return edge.Render(l + strings.Join(parts, m) + r)
	}
	line := func(cells []string, mark string, style *lipgloss.Style) string {
		var s strings.Builder
		s.WriteString(edge.Render(b.v))
		for i, n := range w {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			lead := " "
			if i == 0 {
				lead = mark
			}
			cell := lead + pad(n, ansi.Truncate(c, n, "…")) + " "
			if style != nil {
				cell = style.Render(cell)
			}
			s.WriteString(cell + edge.Render(b.v))
		}
		return s.String()
	}
	muted := d.Palette.Style(theme.Muted)
	out := []string{rule(b.tl, teeT, b.tr), line(g.Cols, " ", &muted), rule(teeL, cross, teeR)}
	for i := from; i < min(to, len(g.Rows)); i++ {
		switch st, ok := g.rowStyle(i); {
		case i == g.Cursor:
			acc := d.Palette.Style(theme.Accent).Bold(true)
			out = append(out, line(g.Rows[i], ">", &acc))
		case ok:
			out = append(out, line(g.Rows[i], " ", &st))
		default:
			out = append(out, line(g.Rows[i], " ", nil))
		}
	}
	return append(out, rule(b.bl, teeB, b.br))
}

func (g Grid) rowStyle(i int) (lipgloss.Style, bool) {
	if g.Style == nil {
		return lipgloss.Style{}, false
	}
	return g.Style(i)
}

// widths are the columns' content widths: each as wide as its widest
// cell, the last given what is left of width (each column also takes a
// border and a space either side).
func (g Grid) widths(width int) []int {
	w := make([]int, len(g.Cols))
	for i, c := range g.Cols {
		w[i] = lipgloss.Width(c)
		for _, r := range g.Rows {
			if i < len(r) {
				w[i] = max(w[i], lipgloss.Width(r[i]))
			}
		}
	}
	last := len(w) - 1
	used := 1
	for _, n := range w[:last] {
		used += n + 3
	}
	w[last] = max(width-used-3, lipgloss.Width(g.Cols[last]))
	return w
}

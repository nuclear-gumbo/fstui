// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Braille dot bits, bottom row first, for the left and right column of a
// cell (Unicode braille patterns start at U+2800).
var (
	brailleLeft  = [4]rune{0x40, 0x04, 0x02, 0x01}
	brailleRight = [4]rune{0x80, 0x20, 0x10, 0x08}
)

// asciiLevels draw the top of a column in ASCII mode, one to four
// quarters of a cell; full cells below the top are asciiFull.
var asciiLevels = [5]string{" ", "_", ".", "-", "'"}

const (
	asciiFull = "|"
	quarters  = 4 // braille dots per column, ASCII levels per cell
)

// GraphSamples is how many values a graph width cells wide shows: two per
// cell in braille, one per cell in ASCII mode.
func GraphSamples(d Display, width int) int {
	if d.ASCII {
		return width
	}
	return 2 * width
}

// Graph draws values as a filled area graph, height rows by width cells, btop
// style. Braille cells hold two samples side by side and four levels each, so a
// four-row graph has sixteen levels; ASCII mode has one sample per cell. Values
// run from 0 to top; NaN is a missing sample. Zero and missing samples draw
// nothing, and any value above zero draws at least one dot, so "a little" never
// looks like "none". The newest value is at the right edge; older values than
// fit are dropped, and a short series leaves the left side empty.
func Graph(d Display, values []float64, top float64, width, height int) string {
	n := GraphSamples(d, width)
	levels := make([]int, n)
	start := n - len(values)
	for i := max(start, 0); i < n; i++ {
		levels[i] = level(values[i-start], top, height*quarters)
	}
	steps := height * quarters
	rows := make([]string, height)
	for r := range height {
		base := (height - 1 - r) * quarters // levels below this row
		var b strings.Builder
		for c := range width {
			cell, reach := "", 0
			if d.ASCII {
				cell, reach = asciiCell(levels[c]-base), levels[c]
			} else {
				cell, reach = string(brailleCell(levels[2*c]-base, levels[2*c+1]-base)), max(levels[2*c], levels[2*c+1])
			}
			if cell == " " {
				b.WriteString(cell)
				continue
			}
			// The colour follows how high this cell's fill reaches.
			reach = min(reach, base+quarters)
			b.WriteString(d.Palette.Graph(reach * percent / steps).Render(cell))
		}
		rows[r] = b.String()
	}
	return strings.Join(rows, "\n")
}

const percent = 100

// level maps v to 0..steps; values above zero get at least 1.
func level(v, top float64, steps int) int {
	if math.IsNaN(v) || v <= 0 || top <= 0 {
		return 0
	}
	return min(int(math.Ceil(v/top*float64(steps))), steps)
}

// brailleCell is the cell for two columns filled to left and right
// dots; an empty cell is a space, because some fonts draw the blank
// braille pattern as faint dots.
func brailleCell(left, right int) rune {
	if left <= 0 && right <= 0 {
		return ' '
	}
	r := rune(0x2800)
	for i := range min(max(left, 0), quarters) {
		r |= brailleLeft[i]
	}
	for i := range min(max(right, 0), quarters) {
		r |= brailleRight[i]
	}
	return r
}

func asciiCell(lv int) string {
	switch {
	case lv <= 0:
		return " "
	case lv > quarters:
		return asciiFull
	}
	return asciiLevels[lv]
}

// eighths are the block elements for bars, one to eight eighths of a
// cell high.
var eighths = [9]string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

const eighthSteps = 8

// Bars draws values as columns of block elements, one sample per cell, height
// rows tall, newest at the right edge, all in style. One colour for the whole
// chart: colour that changes with each cell's value turns a busy series into
// noise. ASCII mode uses Graph's four levels per row. Zero and missing samples
// draw nothing; any value above zero draws at least the lowest bar. Values run
// from 0 to top.
func Bars(d Display, values []float64, top float64, width, height int, style lipgloss.Style) string {
	per := eighthSteps
	if d.ASCII {
		per = quarters
	}
	levels := make([]int, width)
	start := width - len(values)
	for c := max(start, 0); c < width; c++ {
		levels[c] = level(values[c-start], top, height*per)
	}
	rows := make([]string, height)
	for r := range height {
		base := (height - 1 - r) * per // levels below this row
		var b strings.Builder
		for _, lv := range levels {
			n := min(max(lv-base, 0), per)
			switch {
			case n == 0:
				b.WriteString(" ")
			case d.ASCII:
				b.WriteString(asciiCell(lv - base)) // a full cell below the top is |
			default:
				b.WriteString(eighths[n])
			}
		}
		rows[r] = style.Render(b.String())
	}
	return strings.Join(rows, "\n")
}

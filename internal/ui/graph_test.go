// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

func TestGraphBraille(t *testing.T) {
	d := asciiDisplay(false)
	// One row, four levels per sample, two samples per cell:
	// (0, NaN) draw nothing; 0.01 of 1 still shows one dot, 1 fills its
	// column; 0.5 and 0.75 are two and three dots from the bottom.
	got := []rune(ansi.Strip(Graph(d, []float64{0, math.NaN(), 0.01, 1, 0.5, 0.75}, 1, 3, 1)))
	want := []rune{' ', 0x2800 | 0x40 | 0x80 | 0x20 | 0x10 | 0x08, 0x2800 | 0x40 | 0x04 | 0x80 | 0x20 | 0x10}
	if string(got) != string(want) {
		t.Errorf("cells = %U, want %U", got, want)
	}
}

func TestGraphRowsAndShortSeries(t *testing.T) {
	d := asciiDisplay(false)
	// Two rows (eight levels); one full sample at the right edge.
	rows := strings.Split(ansi.Strip(Graph(d, []float64{8}, 8, 4, 2)), "\n")
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	for i, r := range rows {
		cells := []rune(r)
		if len(cells) != 4 || cells[0] != ' ' || cells[3] != 0x2800|0x80|0x20|0x10|0x08 {
			t.Errorf("row %d = %U", i, cells)
		}
	}
	// More values than fit: only the newest 2*width are drawn.
	many := make([]float64, 100)
	many[99] = 1
	if r := []rune(ansi.Strip(Graph(d, many, 1, 2, 1))); len(r) != 2 || r[1] != 0x2800|0x80|0x20|0x10|0x08 || r[0] != ' ' {
		t.Errorf("newest at the right: %U", r)
	}
}

func TestGraphASCII(t *testing.T) {
	// Two rows of four levels, one sample per cell: a full cell below the
	// top is "|", the top shows its level with _ . - and '.
	got := ansi.Strip(Graph(asciiDisplay(true), []float64{0, 1, 4, 5, 8}, 8, 5, 2))
	if want := "   _'\n _'||"; got != want {
		t.Errorf("ASCII graph:\n%s\nwant\n%s", got, want)
	}
}

// Under true colour a full column is dim at the bottom and bright at the
// top; on 16 colours the graph is the accent, flat.
func TestGraphGradient(t *testing.T) {
	d := Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.TrueColor, nil)}
	rows := strings.Split(Graph(d, []float64{1, 1}, 1, 1, 3), "\n")
	if rows[0] == rows[2] || !strings.Contains(rows[0], "38;2;") {
		t.Errorf("top %q and bottom %q of a full column look the same", rows[0], rows[2])
	}
	flat := Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.ANSI, nil)}
	rows = strings.Split(Graph(flat, []float64{1, 1}, 1, 1, 3), "\n")
	if rows[0] != rows[2] {
		t.Errorf("16 colours: %q and %q differ", rows[0], rows[2])
	}
}

// Bars: one sample per cell, eight levels per row, newest at the right,
// and one style for every cell, whatever its height.
func TestBars(t *testing.T) {
	d := asciiDisplay(false)
	got := ansi.Strip(Bars(d, []float64{0.01, 0.5, 1, math.NaN()}, 1, 6, 2, lipgloss.NewStyle()))
	want := "    █ \n  ▁██ "
	if got != want {
		t.Errorf("bars =\n%q, want\n%q", got, want)
	}
	if a := Bars(asciiDisplay(true), []float64{1, 0.25}, 1, 2, 2, lipgloss.NewStyle()); ansi.Strip(a) != "' \n|." {
		t.Errorf("ASCII bars = %q", ansi.Strip(a))
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

var iecUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// oneDecimalBelow: sizes under 100 of their unit get one decimal place,
// larger ones none.
const oneDecimalBelow = 100

// Size formats a byte count in IEC units, or exactly with digits grouped
// in threes when exact is set.
func Size(n uint64, exact bool) string {
	if exact {
		return grouped(n)
	}
	v, unit := float64(n), 0
	// 1023.7 MiB would print as "1024 MiB"; carry into the next unit.
	for v >= 1024-0.5 && unit < len(iecUnits)-1 {
		v /= 1024
		unit++
	}
	switch {
	case unit == 0:
		return fmt.Sprintf("%d B", n)
	case v < oneDecimalBelow && fmt.Sprintf("%.1f", v) != "100.0":
		return fmt.Sprintf("%.1f %s", v, iecUnits[unit])
	}
	return fmt.Sprintf("%.0f %s", v, iecUnits[unit])
}

func grouped(n uint64) string {
	s := strconv.FormatUint(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// Bar sizes.
const (
	BarWidth    = 26
	BarMinWidth = 10
)

// Level is how a value compares with its configured thresholds.
type Level int

// Levels.
const (
	Normal Level = iota
	Warning
	Critical
)

// LevelOf compares a percentage with the warn and error thresholds.
func LevelOf(pct, warn, crit int) Level {
	switch {
	case pct >= crit:
		return Critical
	case pct >= warn:
		return Warning
	}
	return Normal
}

// Bar draws a thin meter of width cells filled to pct percent. Each filled cell
// takes the theme's meter colour for its position along the bar, btop style,
// anchored at the warn and error thresholds: a nearly full pool shows the error
// colour at its end, an emptier one never reaches it. The level also appears as
// a word beside the bar, so colour never carries it alone.
func Bar(d Display, pct, width, warn, crit int) string {
	pct = min(max(pct, 0), 100)
	filled := (pct*width + 50) / 100
	fill, track := "━", "─"
	if d.ASCII {
		fill, track = "#", "."
	}
	var b strings.Builder
	for i := range filled {
		// Colour by the middle of the cell, so a 10-cell bar's first cell
		// is at 5%, not 0%.
		pos := (2*i + 1) * 100 / (2 * width)
		b.WriteString(d.Palette.Meter(pos, warn, crit).Render(fill))
	}
	return b.String() + d.Palette.Style(theme.Border).Render(strings.Repeat(track, width-filled))
}

// Gauge draws a thin meter like Bar in the accent colour, for amounts
// where full is not a problem, such as a cache at its target size.
func Gauge(d Display, pct, width int) string {
	pct = min(max(pct, 0), 100)
	filled := (pct*width + 50) / 100
	fill, track := "━", "─"
	if d.ASCII {
		fill, track = "#", "."
	}
	return d.Palette.Style(theme.Accent).Render(strings.Repeat(fill, filled)) +
		d.Palette.Style(theme.Border).Render(strings.Repeat(track, width-filled))
}

// LevelWord is shown beside a bar over a threshold: WARN, or FULL from the
// error threshold.
func LevelWord(l Level) string {
	return map[Level]string{Normal: "", Warning: "WARN", Critical: "FULL"}[l]
}

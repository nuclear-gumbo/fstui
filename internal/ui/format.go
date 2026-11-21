// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

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

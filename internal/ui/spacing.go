// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Pad indents every line of s by x columns and puts y blank lines above
// it: the margin inside a frame.
func Pad(s string, x, y int) string {
	lines := strings.Split(s, "\n")
	ind := strings.Repeat(" ", x)
	for i, l := range lines {
		if l != "" {
			lines[i] = ind + l
		}
	}
	return strings.Repeat("\n", y) + strings.Join(lines, "\n")
}

// Box frames lines under a title, one column of padding inside, in at
// most width cells: a region of its own, for the description of a
// selection or a group of related lines (Laws of UX, common region).
func Box(d Display, title string, lines []string, width int) []string {
	var inner []string
	for _, l := range lines { // a wrapped paragraph is several lines
		for _, part := range strings.Split(l, "\n") {
			inner = append(inner, " "+part)
		}
	}
	w := width
	if w > 4 {
		widest := 0
		for _, l := range inner {
			widest = max(widest, lipgloss.Width(l))
		}
		w = min(width, max(widest+3, lipgloss.Width(title)+6))
	}
	out := Panel{Title: title}.Render(d, strings.Join(inner, "\n"), w, len(inner)+2)
	return strings.Split(out, "\n")
}

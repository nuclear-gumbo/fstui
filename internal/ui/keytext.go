// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Keys named in sentences ("space turns one on or off") are drawn as in
// the key bar, in the theme's key colour, which is none of the others, so
// a key stands apart from the words, headings and selection around it. Text marks a key with keyName or keyMark when it is
// built, often far from a Display; styleKeys colours the marks when the
// view is drawn. The marks are control characters, which take no width,
// so wrapping and truncating before styleKeys stay right.
const (
	keyOpen  = "\x1e"
	keyClose = "\x1f"
)

// keyName is the first key bound to a, marked as a key.
func keyName(a keys.Action) string { return keyMark(keys.Get(a).Keys()[0]) }

// keyMark marks k as a key.
func keyMark(k string) string { return keyOpen + k + keyClose }

// styleKeys draws the marked keys in s; a mark cut off by truncation is
// closed at the end of its line.
func styleKeys(d Display, s string) string {
	if !strings.Contains(s, keyOpen) {
		return s
	}
	st := d.Palette.Style(theme.Key).Bold(true)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		var b strings.Builder
		for {
			open := strings.Index(l, keyOpen)
			if open < 0 {
				b.WriteString(strings.ReplaceAll(l, keyClose, ""))
				break
			}
			before := l[:open]
			b.WriteString(before)
			l = l[open+len(keyOpen):]
			end := strings.Index(l, keyClose)
			if end < 0 {
				end = len(l)
			}
			// The key's style ends with a reset; the text after it goes
			// on in the style it was in (a muted sentence stays muted).
			b.WriteString(st.Render(l[:end]) + activeStyle(b.String()))
			l = strings.TrimPrefix(l[end:], keyClose)
		}
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n")
}

// activeStyle is the last SGR sequence in s, the style text written after
// s is in, or "" when that is none (s ends reset or has no style).
func activeStyle(s string) string {
	i := strings.LastIndex(s, "\x1b[")
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(s[i:], 'm')
	if j < 0 {
		return ""
	}
	seq := s[i : i+j+1]
	if seq == "\x1b[m" || seq == "\x1b[0m" {
		return ""
	}
	return seq
}

// WrapStyled wraps styled text to width and starts every line after the
// first in the style the line before it ended in: a frame drawn in front
// of each line resets the style, so a wrapped warning would otherwise
// lose its colour after its first line.
func WrapStyled(s string, width int) []string {
	lines := strings.Split(ansi.Wrap(s, max(width, 1), ""), "\n")
	for i := 1; i < len(lines); i++ {
		if st := activeStyle(lines[i-1]); st != "" && !strings.HasPrefix(lines[i], st) {
			lines[i] = st + lines[i]
		}
	}
	return lines
}

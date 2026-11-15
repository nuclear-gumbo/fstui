// SPDX-License-Identifier: GPL-3.0-or-later

// Package termtext carries text that came from outside fstui and makes it
// safe to show in a terminal.
//
// ZFS accepts raw escape sequences, carriage returns and other control
// characters in user property values and hold tags, and echoes hostile bytes
// back in its error messages. Rendering such a string unmodified lets it clear
// the screen, retitle the terminal, write the clipboard, or overwrite a name
// with another. Text is therefore always sanitised with an allowlist: printable
// characters pass, everything else is shown as a visible escape. Nothing is
// interpreted or simulated.
package termtext

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text is a string from an untrusted source: ZFS output, stderr, device
// metadata. The zero value is the empty string.
type Text struct {
	raw string
}

// New wraps an untrusted string.
func New(s string) Text { return Text{raw: s} }

// Raw returns the original bytes, for passing back to a command as an
// argument. It must never be rendered.
func (t Text) Raw() string { return t.raw }

// IsEmpty reports whether the text is the empty string.
func (t Text) IsEmpty() bool { return t.raw == "" }

// FirstLine returns the first non-blank line, for one-line summaries of
// multi-line messages such as stderr. It splits before sanitising, since
// Display shows a newline as a visible escape.
func (t Text) FirstLine() Text {
	for _, l := range strings.Split(t.raw, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return Text{raw: l}
		}
	}
	return Text{}
}

// Segment is a run of display text. Escaped segments stand for characters
// that were not printed as-is; the UI renders them in a distinct style.
type Segment struct {
	Text    string
	Escaped bool
}

// Segments splits the text into printable runs and visible escapes. Adjacent
// characters of the same kind are merged into one segment.
func (t Text) Segments() []Segment {
	var segs []Segment
	var cur strings.Builder
	curEscaped := false
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, Segment{Text: cur.String(), Escaped: curEscaped})
			cur.Reset()
		}
	}
	s := t.raw
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		var piece string
		escaped := false
		switch {
		case r == utf8.RuneError && size == 1:
			piece, escaped = fmt.Sprintf(`\x%02x`, s[0]), true
		case !printable(r):
			piece, escaped = escape(r), true
		default:
			piece = s[:size]
		}
		if escaped != curEscaped {
			flush()
			curEscaped = escaped
		}
		cur.WriteString(piece)
		s = s[size:]
	}
	flush()
	return segs
}

// Display returns the text with every unsafe character replaced by its
// visible escape. The result is safe to write to a terminal. It is ambiguous
// when the original contains a literal backslash sequence; use Segments when
// the difference matters.
func (t Text) Display() string {
	var b strings.Builder
	for _, seg := range t.Segments() {
		b.WriteString(seg.Text)
	}
	return b.String()
}

// String implements fmt.Stringer with the sanitised form, so that a Text
// passed to a formatting function by mistake is still safe.
func (t Text) String() string { return t.Display() }

// printable reports whether r may be written to a terminal as-is.
func printable(r rune) bool {
	switch {
	case r == ' ':
		return true
	case unicode.Is(unicode.Cf, r):
		// Format characters: bidi overrides and isolates (U+202A-U+202E,
		// U+2066-U+2069), zero-width space and joiners, BOM, soft hyphen.
		// They are invisible or reorder text, so they can disguise a name.
		return false
	case unicode.IsSpace(r):
		// Every space other than ASCII space: tabs, newlines, NBSP and other
		// look-alike spaces, line and paragraph separators.
		return false
	default:
		// IsGraphic excludes C0/C1 controls, DEL, private use and unassigned
		// code points.
		return unicode.IsGraphic(r)
	}
}

// escape returns the visible form of a character that is not printed as-is.
func escape(r rune) string {
	switch r {
	case '\a':
		return `\a`
	case '\b':
		return `\b`
	case '\t':
		return `\t`
	case '\n':
		return `\n`
	case '\v':
		return `\v`
	case '\f':
		return `\f`
	case '\r':
		return `\r`
	}
	if r < 0x100 {
		return fmt.Sprintf(`\x%02x`, r)
	}
	if r <= 0xFFFF {
		return fmt.Sprintf(`\u%04x`, r)
	}
	return fmt.Sprintf(`\U%08x`, r)
}

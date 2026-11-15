// SPDX-License-Identifier: GPL-3.0-or-later

package termtext

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestDisplay(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain dataset name", "tank/home@daily-2026-11-12", "tank/home@daily-2026-11-12"},
		{"space and ZFS punctuation", "tank/with space:a.b_c-d", "tank/with space:a.b_c-d"},
		{"non-ASCII letters stay", "caf\u00e9 \u00fcn\u00efcode \u540d\u524d", "caf\u00e9 \u00fcn\u00efcode \u540d\u524d"},
		{"combining mark stays", "e\u0301", "e\u0301"},
		// Values accepted by ZFS 2.3.9 in user properties.
		{"clear screen", "evil\x1b[2J", `evil\x1b[2J`},
		{"OSC title with BEL", "\x1b]0;pwned\x07", `\x1b]0;pwned\a`},
		{"OSC 52 clipboard write", "\x1b]52;c;aGk=\x07", `\x1b]52;c;aGk=\a`},
		{"carriage return overwrite is shown, not applied", "tank/scratch\rtank/home", `tank/scratch\rtank/home`},
		{"newline would forge a row", "line1\nline2", `line1\nline2`},
		{"tab", "a\tb", `a\tb`},
		{"NUL", "a\x00b", `a\x00b`},
		{"DEL", "a\x7fb", `a\x7fb`},
		{"C1 CSI introducer", "a\u009b2Jb", `a\x9b2Jb`},
		{"bidi override reorders text", "tank/\u202egnp.exe", `tank/\u202egnp.exe`},
		{"bidi isolate", "a\u2066b\u2069", `a\u2066b\u2069`},
		{"zero-width space", "tank\u200bhome", `tank\u200bhome`},
		{"zero-width joiner", "a\u200db", `a\u200db`},
		{"byte order mark", "\ufeffname", `\ufeffname`},
		{"soft hyphen", "a\u00adb", `a\xadb`},
		{"no-break space looks like a space", "tank\u00a0home", `tank\xa0home`},
		{"ideographic space", "a\u3000b", `a\u3000b`},
		{"line separator", "a\u2028b", `a\u2028b`},
		{"private use", "a\ue000b", `a\ue000b`},
		{"invalid UTF-8 byte", "a\xffb", `a\xffb`},
		{"truncated UTF-8 sequence", "a\xe2\x82", `a\xe2\x82`},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(tt.in).Display(); got != tt.want {
				t.Errorf("Display(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSegmentsMarkEscapes(t *testing.T) {
	got := New("ok\x1b[31mred").Segments()
	want := []Segment{{"ok", false}, {`\x1b`, true}, {"[31mred", false}}
	if len(got) != len(want) {
		t.Fatalf("Segments() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Segments()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAdjacentEscapesMerge(t *testing.T) {
	got := New("\r\n").Segments()
	if len(got) != 1 || got[0] != (Segment{`\r\n`, true}) {
		t.Fatalf("Segments() = %+v, want one escaped segment", got)
	}
}

func TestRawKeepsOriginalBytes(t *testing.T) {
	in := "tag\x1b[1m"
	if got := New(in).Raw(); got != in {
		t.Fatalf("Raw() = %q, want %q", got, in)
	}
}

func TestStringIsSanitised(t *testing.T) {
	if got := New("a\x1bb").String(); strings.ContainsRune(got, 0x1b) {
		t.Fatalf("String() contains ESC: %q", got)
	}
}

// FuzzDisplayIsSafe checks the invariant the package exists for: whatever
// the input, the output is valid UTF-8 and contains nothing but printable
// characters and ASCII space.
func FuzzDisplayIsSafe(f *testing.F) {
	for _, s := range []string{
		"tank/home", "\x1b[2J", "\x1b]0;x\x07", "a\rb", "\u202e", "\u200d",
		"\xff\xfe", "\u00a0", "\x9b", "\u540d\u524d", "e\u0301",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := New(in).Display()
		if !utf8.ValidString(out) {
			t.Fatalf("Display(%q) is not valid UTF-8: %q", in, out)
		}
		for _, r := range out {
			if r == ' ' {
				continue
			}
			if !unicode.IsGraphic(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
				t.Fatalf("Display(%q) contains unsafe rune %U: %q", in, r, out)
			}
		}
	})
}

func TestFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"cannot open 'tank': no such pool\nsecond": "cannot open 'tank': no such pool",
		"\n  \n  indented\n":                       "indented",
		"":                                         "",
		"evil\x1b[2J\nnext":                        `evil\x1b[2J`,
	} {
		if got := New(in).FirstLine().Display(); got != want {
			t.Errorf("FirstLine(%q) = %q, want %q", in, got, want)
		}
	}
}

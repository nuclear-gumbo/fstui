// SPDX-License-Identifier: GPL-3.0-or-later

package name

import (
	"strings"
	"testing"
	"unicode"
)

// A name that validates is safe to put after "--" on a command line and to
// show: no control characters, no leading "-", within ZFS's length.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"tank", "tank/home@daily-1", "tank/a#b", "-x", "tank/\x1b[2J", "tank/a b", "a/../b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, parse := range []func(string) (string, error){
			func(s string) (string, error) { p, err := ParsePool(s); return p.String(), err },
			func(s string) (string, error) { d, err := ParseDataset(s); return d.String(), err },
			func(s string) (string, error) { d, err := ParseSnapshot(s); return d.String(), err },
			func(s string) (string, error) { d, err := ParseBookmark(s); return d.String(), err },
		} {
			got, err := parse(s)
			if err != nil {
				continue
			}
			if got != s || strings.HasPrefix(got, "-") || len(got) > MaxLen ||
				strings.IndexFunc(got, func(r rune) bool { return unicode.IsControl(r) || r > unicode.MaxASCII }) >= 0 {
				t.Errorf("accepted %q", s)
			}
		}
	})
}

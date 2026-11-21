// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// A key inside a muted sentence leaves the rest of the sentence muted.
func TestStyleKeysKeepsTheSentenceStyle(t *testing.T) {
	d := Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.TrueColor, nil)}
	muted := d.Palette.Style(theme.Muted)
	out := styleKeys(d, muted.Render("Press "+keyMark("space")+" to turn it on."))
	after := out[strings.Index(out, "space")+len("space"):]
	mutedStart := muted.Render("x")[:strings.Index(muted.Render("x"), "x")]
	if !strings.Contains(after, mutedStart) {
		t.Errorf("the text after the key is not muted: %q", out)
	}
	if strings.ContainsAny(out, keyOpen+keyClose) {
		t.Errorf("marks left: %q", out)
	}
}

// Every wrapped line of a coloured sentence keeps the colour.
func TestWrapStyled(t *testing.T) {
	d := Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.TrueColor, nil)}
	muted := d.Palette.Style(theme.Muted)
	start := muted.Render("x")[:strings.Index(muted.Render("x"), "x")]
	lines := WrapStyled(muted.Render("No scrub is scheduled: no OpenZFS scrub timer or Debian cron job covers this pool."), 30)
	if len(lines) < 3 {
		t.Fatalf("lines %q", lines)
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, start) {
			t.Errorf("line %d lost the colour: %q", i, l)
		}
	}
}

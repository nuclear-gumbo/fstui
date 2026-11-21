// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A wrapped paragraph in a box keeps its indent on every line, and no
// line is cut off.
func TestBoxWrapped(t *testing.T) {
	out := Box(asciiDisplay(true), "About", []string{"first line\nsecond line", "third"}, 40)
	got := ansi.Strip(strings.Join(out, "\n"))
	for _, want := range []string{"| first line", "| second line", "| third"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if len(out) != 5 {
		t.Errorf("%d lines, want 5:\n%s", len(out), got)
	}
}

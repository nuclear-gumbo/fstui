// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

func asciiDisplay(ascii bool) Display {
	return Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.ASCII, nil), ASCII: ascii}
}

func TestPanelFrame(t *testing.T) {
	p := Panel{Title: "pools", Tabs: []string{"Topology", "Capacity"}, Active: 1}
	out := ansi.Strip(p.Render(asciiDisplay(false), "line one\n"+strings.Repeat("x", 100)+"\nthree\nfour\nfive", 40, 5))
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("%d lines, want 5", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 40 {
			t.Errorf("line %d is %d wide: %q", i, w, l)
		}
	}
	if !strings.HasPrefix(lines[0], "╭─pools│ Topology │ Capacity │─") || !strings.HasSuffix(lines[0], "╮") {
		t.Errorf("top = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "│line one") || !strings.HasSuffix(lines[2], "…│") {
		t.Errorf("body clipping: %q / %q", lines[1], lines[2])
	}
	if !strings.HasPrefix(lines[4], "╰") || !strings.Contains(out, "three") || strings.Contains(out, "four") {
		t.Errorf("body rows wrong:\n%s", out)
	}
}

func TestPanelASCII(t *testing.T) {
	out := ansi.Strip(Panel{Title: "settings"}.Render(asciiDisplay(true), "x", 20, 3))
	if !strings.HasPrefix(out, "+-settings---") || strings.ContainsAny(out, "╭╮╰╯│─") {
		t.Fatalf("ASCII panel:\n%s", out)
	}
}

func TestPanelTabAt(t *testing.T) {
	d := asciiDisplay(false)
	p := Panel{Title: "data", Tabs: []string{"Topology", "Capacity", "History"}}
	top := ansi.Strip(strings.Split(p.Render(d, "", 80, 3), "\n")[0])
	for i, name := range p.Tabs {
		x := strings.Index(top, name) // byte offset equals cells: the prefix is ASCII but for ╭─¹
		cells := lipgloss.Width(top[:x])
		if got := p.TabAt(80, cells); got != i {
			t.Errorf("click on %s at %d = tab %d", name, cells, got)
		}
	}
	if got := p.TabAt(80, 3); got != -1 {
		t.Errorf("click on the title = tab %d", got)
	}
}

// A narrow panel shortens its title so every tab stays whole and
// clickable.
func TestPanelShortensTitleForTabs(t *testing.T) {
	d := asciiDisplay(false)
	p := Panel{Title: "fstuitest_g_healthy", Tabs: []string{"Topology", "Capacity", "Properties", "Features", "History"}}
	top := ansi.Strip(strings.Split(p.Render(d, "", 70, 3), "\n")[0])
	if lipgloss.Width(top) != 70 || !strings.Contains(top, " History ") || !strings.Contains(top, "fstu") || !strings.Contains(top, "…") {
		t.Fatalf("top edge: %q", top)
	}
	x := strings.Index(top, "History")
	if got := p.TabAt(70, lipgloss.Width(top[:x])); got != 4 {
		t.Errorf("click on History = tab %d", got)
	}
}

// View numbers in the view bar: superscripts, "9:" in ASCII mode, and 0
// for the tenth view.
func TestViewNumber(t *testing.T) {
	for _, c := range []struct {
		n     int
		ascii bool
		want  string
	}{{1, false, "¹"}, {9, true, "9:"}, {10, false, "⁰"}, {10, true, "0:"}, {0, false, ""}, {11, false, ""}} {
		if got := viewNumber(asciiDisplay(c.ascii), c.n); got != c.want {
			t.Errorf("viewNumber(%d, ascii %v) = %q, want %q", c.n, c.ascii, got, c.want)
		}
	}
}

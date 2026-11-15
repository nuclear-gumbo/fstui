// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

const esc = "\x1b["

func TestResolveByProfile(t *testing.T) {
	for _, tc := range []struct {
		profile    colorprofile.Profile
		dark       bool
		token      Token
		want, deny string
	}{
		{colorprofile.TrueColor, true, Accent, "38;2;124;156;180", ""},
		{colorprofile.TrueColor, false, Accent, "38;2;63;103;130", ""},
		{colorprofile.ANSI256, true, Warn, "38;5;179", ""},
		{colorprofile.ANSI256, false, Warn, "38;5;94", ""},
		{colorprofile.ANSI, true, Error, esc + "31m", ""},
		// Muted is faint text in the default colour at 16 colours and
		// without colour, never ANSI 8, which light themes hide.
		{colorprofile.ANSI, true, Muted, esc + "2m", "38;"},
		{colorprofile.ASCII, true, Muted, esc + "2m", "38;"},
		{colorprofile.ASCII, true, Error, "", esc},
		{colorprofile.NoTTY, true, Muted, "", esc},
	} {
		got := Resolve(Theme{}, tc.dark, tc.profile, nil).Style(tc.token).Render("x")
		if tc.want != "" && !strings.Contains(got, tc.want) || tc.deny != "" && strings.Contains(got, tc.deny) {
			t.Errorf("%v dark=%v %s: rendered %q, want %q and not %q", tc.profile, tc.dark, tc.token, got, tc.want, tc.deny)
		}
	}
}

func TestResolveNeverPaintsBackground(t *testing.T) {
	for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI} {
		for tok := Token(0); tok < numTokens; tok++ {
			got := Resolve(Theme{}, true, profile, nil).Style(tok).Render("x")
			if strings.Contains(got, "48;") || strings.Contains(got, esc+"4") && !strings.Contains(got, esc+"4m") {
				t.Errorf("%v %s sets a background: %q", profile, tok, got)
			}
		}
	}
}

func TestResolveOverride(t *testing.T) {
	red := RGB{0xFF, 0, 0}
	o := map[Token]Override{Accent: {Dark: &red}}
	if got := Resolve(Theme{}, true, colorprofile.TrueColor, o).Style(Accent).Render("x"); !strings.Contains(got, "38;2;255;0;0") {
		t.Errorf("truecolor override: %q", got)
	}
	// xterm index 196 is pure red (#FF0000).
	if got := Resolve(Theme{}, true, colorprofile.ANSI256, o).Style(Accent).Render("x"); !strings.Contains(got, "38;5;196") {
		t.Errorf("256-colour override: %q, want the nearest index 196", got)
	}
	if got := Resolve(Theme{}, false, colorprofile.TrueColor, o).Style(Accent).Render("x"); !strings.Contains(got, "38;2;63;103;130") {
		t.Errorf("a dark-only override changed the light value: %q", got)
	}
	if got := Resolve(Theme{}, true, colorprofile.ANSI, o).Style(Accent).Render("x"); !strings.Contains(got, esc+"34m") {
		t.Errorf("16-colour value should stay blue: %q", got)
	}
}

func TestNearest256ExactEntries(t *testing.T) {
	for _, i := range []uint8{16, 110, 179, 231, 232, 255} {
		c, _ := ANSI256RGB(i)
		if got := Nearest256(c); got != i {
			t.Errorf("Nearest256(%v) = %d, want %d", c, got, i)
		}
	}
}

func TestThemeSetsAndFallback(t *testing.T) {
	blue := RGB{0x12, 0x34, 0x56}
	th := Theme{Name: "x", Dark: &Set{Colors: [numTokens]*RGB{Accent: &blue}}}
	if got := Resolve(th, true, colorprofile.TrueColor, nil).Style(Accent).Render("x"); !strings.Contains(got, "38;2;18;52;86") {
		t.Errorf("theme colour not used: %q", got)
	}
	if got := Resolve(th, true, colorprofile.TrueColor, nil).Style(Warn).Render("x"); !strings.Contains(got, "38;2;210;178;110") {
		t.Errorf("a token the theme omits should keep the default: %q", got)
	}
	// No light set: the default light colours apply.
	if got := Resolve(th, false, colorprofile.TrueColor, nil).Style(Accent).Render("x"); !strings.Contains(got, "38;2;63;103;130") {
		t.Errorf("light fallback: %q", got)
	}
}

func TestMeter(t *testing.T) {
	const warn, crit = 80, 90
	p := Resolve(Theme{}, true, colorprofile.TrueColor, nil)
	ok, wn, er := Default[OK].Dark.TrueColor, Default[Warn].Dark.TrueColor, Default[Error].Dark.TrueColor
	for _, tc := range []struct {
		pos  int
		want RGB
	}{{0, ok}, {warn, wn}, {crit, er}, {100, er}, {40, mix(ok, wn, 0.5)}} {
		want := fmt.Sprintf("38;2;%d;%d;%d", tc.want.R, tc.want.G, tc.want.B)
		if got := p.Meter(tc.pos, warn, crit).Render("x"); !strings.Contains(got, want) {
			t.Errorf("truecolor meter at %d%%: %q, want %s", tc.pos, got, want)
		}
	}
	// 16 colours: flat bands at the thresholds (green, yellow, red).
	p16 := Resolve(Theme{}, true, colorprofile.ANSI, nil)
	for pos, want := range map[int]string{0: esc + "32m", 79: esc + "32m", 80: esc + "33m", 90: esc + "31m"} {
		if got := p16.Meter(pos, warn, crit).Render("x"); !strings.Contains(got, want) {
			t.Errorf("16-colour meter at %d%%: %q, want %q", pos, got, want)
		}
	}
	if got := Resolve(Theme{}, true, colorprofile.ASCII, nil).Meter(95, warn, crit).Render("x"); got != "x" {
		t.Errorf("no-colour meter: %q", got)
	}
}

func TestBuiltinNames(t *testing.T) {
	names := BuiltinNames()
	if names[0] != DefaultName {
		t.Fatalf("names = %v, want default first", names)
	}
	for _, n := range names[1:] {
		if _, ok := Builtin(n); !ok {
			t.Errorf("listed theme %q cannot be read", n)
		}
	}
	if _, ok := Builtin("../theme"); ok {
		t.Error("a path outside themes/ was readable")
	}
}

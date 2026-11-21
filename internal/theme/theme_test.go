// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"math"
	"testing"
)

// Backgrounds the palette is measured against: a typical dark
// terminal and white.
var (
	darkBackground  = RGB{0x1C, 0x1E, 0x22}
	lightBackground = RGB{0xFF, 0xFF, 0xFF}
)

// textTokens are drawn as text and must be readable; border is
// deliberately faint and never carries information.
var textTokens = []Token{Accent, OK, Warn, Error, Muted}

// Thresholds. WCAG AA for normal text is 4.5:1.
const (
	minContrastDarkTrue  = 5.0
	minContrastLightTrue = 5.3
	minContrast256       = 4.5
	// Smallest ΔE76 between text tokens; these are the measured
	// values 12.4 / 14.0 (truecolor) and 26.2 / 30.2 (256 colours).
	minDeltaEDarkTrue  = 12.4
	minDeltaELightTrue = 14.0
	minDeltaEDark256   = 26.2
	minDeltaELight256  = 30.2
	// The documented figures are rounded to one decimal.
	measuredTolerance = 0.05
)

type depth struct {
	name  string
	color func(Value) RGB
}

var depths = []depth{
	{"truecolor", func(v Value) RGB { return v.TrueColor }},
	{"256", func(v Value) RGB { c, _ := ANSI256RGB(v.ANSI256); return c }},
}

func TestANSI256Palette(t *testing.T) {
	for _, tc := range []struct {
		i    uint8
		want RGB
	}{
		{16, RGB{0, 0, 0}},
		{21, RGB{0, 0, 255}},
		{110, RGB{0x87, 0xAF, 0xD7}},
		{231, RGB{255, 255, 255}},
		{232, RGB{8, 8, 8}},
		{255, RGB{0xEE, 0xEE, 0xEE}},
	} {
		if got, ok := ANSI256RGB(tc.i); !ok || got != tc.want {
			t.Errorf("ANSI256RGB(%d) = %v, want %v", tc.i, got, tc.want)
		}
	}
	if _, ok := ANSI256RGB(15); ok {
		t.Error("system colour 15 reported a fixed value")
	}
}

// The hex value of each 256-colour index the palette uses.
func TestDocumentedANSI256Values(t *testing.T) {
	want := map[uint8]RGB{
		110: {0x87, 0xAF, 0xD7}, 24: {0x00, 0x5F, 0x87},
		108: {0x87, 0xAF, 0x87}, 22: {0x00, 0x5F, 0x00},
		179: {0xD7, 0xAF, 0x5F}, 94: {0x87, 0x5F, 0x00},
		167: {0xD7, 0x5F, 0x5F}, 131: {0xAF, 0x5F, 0x5F},
		246: {0x94, 0x94, 0x94}, 241: {0x62, 0x62, 0x62},
		239: {0x4E, 0x4E, 0x4E}, 252: {0xD0, 0xD0, 0xD0},
		182: {0xD7, 0xAF, 0xD7}, 96: {0x87, 0x5F, 0x87},
	}
	for _, p := range Default {
		for _, v := range []Value{p.Dark, p.Light} {
			if got, _ := ANSI256RGB(v.ANSI256); got != want[v.ANSI256] {
				t.Errorf("index %d renders as %v, want %v", v.ANSI256, got, want[v.ANSI256])
			}
		}
	}
}

func TestContrast(t *testing.T) {
	for _, tok := range textTokens {
		p := Default[tok]
		checks := []struct {
			what  string
			value RGB
			bg    RGB
			min   float64
		}{
			{"dark truecolor", p.Dark.TrueColor, darkBackground, minContrastDarkTrue},
			{"light truecolor", p.Light.TrueColor, lightBackground, minContrastLightTrue},
			{"dark 256", depths[1].color(p.Dark), darkBackground, minContrast256},
			{"light 256", depths[1].color(p.Light), lightBackground, minContrast256},
		}
		for _, c := range checks {
			if got := Contrast(c.value, c.bg); got < c.min {
				t.Errorf("%s %s %v: contrast %.2f:1, want at least %.1f:1", tok, c.what, c.value, got, c.min)
			}
		}
	}
}

// minDistance returns the smallest ΔE between any two text tokens.
func minDistance(value func(Pair) RGB) float64 {
	smallest := math.Inf(1)
	for i, a := range textTokens {
		for _, b := range textTokens[i+1:] {
			smallest = math.Min(smallest, DeltaE(value(Default[a]), value(Default[b])))
		}
	}
	return smallest
}

func TestTokensDistinct(t *testing.T) {
	for _, tc := range []struct {
		what  string
		value func(Pair) RGB
		min   float64
	}{
		{"dark truecolor", func(p Pair) RGB { return p.Dark.TrueColor }, minDeltaEDarkTrue},
		{"light truecolor", func(p Pair) RGB { return p.Light.TrueColor }, minDeltaELightTrue},
		{"dark 256", func(p Pair) RGB { return depths[1].color(p.Dark) }, minDeltaEDark256},
		{"light 256", func(p Pair) RGB { return depths[1].color(p.Light) }, minDeltaELight256},
	} {
		if got := minDistance(tc.value); got < tc.min-measuredTolerance {
			t.Errorf("%s: closest text tokens are ΔE %.1f apart, want at least %.1f", tc.what, got, tc.min)
		}
	}
}

func TestStateTokensDistinctIn16Colours(t *testing.T) {
	seen := map[int]Token{}
	for _, tok := range []Token{Accent, OK, Warn, Error} {
		ansi := Default[tok].Dark.ANSI
		if prev, dup := seen[ansi]; dup {
			t.Errorf("%s and %s share ANSI colour %d", prev, tok, ansi)
		}
		seen[ansi] = tok
		if Default[tok].Light.ANSI != ansi {
			t.Errorf("%s: 16-colour index differs between backgrounds", tok)
		}
	}
}

func TestParseHex(t *testing.T) {
	if c, err := ParseHex("#7C9CB4"); err != nil || c != Default[Accent].Dark.TrueColor {
		t.Fatalf("ParseHex(#7C9CB4) = %v, %v", c, err)
	}
	for _, bad := range []string{"", "7C9CB4", "#7C9CB", "#7C9CB4FF", "#GGGGGG", "#-12345"} {
		if c, err := ParseHex(bad); err == nil {
			t.Errorf("ParseHex(%q) = %v, want an error", bad, c)
		}
	}
}

func TestTokenByName(t *testing.T) {
	for tok := Token(0); tok < numTokens; tok++ {
		if got, ok := TokenByName(tok.String()); !ok || got != tok {
			t.Errorf("TokenByName(%q) = %v, %v", tok, got, ok)
		}
	}
	if _, ok := TokenByName("text"); ok {
		t.Error("body text has no token, but text was accepted")
	}
}

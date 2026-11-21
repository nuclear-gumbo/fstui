// SPDX-License-Identifier: GPL-3.0-or-later

// Package theme holds fstui's semantic colour tokens and their values for
// dark and light terminals at each colour depth.
//
// Views refer to tokens, never to colours. Body text has no token: it uses
// the terminal's own foreground, and fstui never sets a background.
package theme

import (
	"fmt"
	"strconv"
)

// Token is a semantic colour role.
type Token int

// Tokens.
const (
	Accent Token = iota // focus, selection, active tab
	OK                  // healthy state words, capacity below threshold
	Warn                // DEGRADED, scrub overdue, capacity over warn, WRITE mode
	Error               // FAULTED, UNAVAIL, errors, failed operations
	Muted               // secondary text, units, commands in previews, visible escapes
	Border              // pane divider, modal border, capacity bar track
	Key                 // a key, in the key bar and wherever text names one
	numTokens
)

var tokenNames = [numTokens]string{"accent", "ok", "warn", "error", "muted", "border", "key"}

func (t Token) String() string { return tokenNames[t] }

// TokenByName returns the token called name in config files, e.g. "warn".
func TokenByName(name string) (Token, bool) {
	for t, n := range tokenNames {
		if n == name {
			return Token(t), true
		}
	}
	return 0, false
}

// RGB is a 24-bit colour.
type RGB struct{ R, G, B uint8 }

// ParseHex parses "#RRGGBB".
func ParseHex(s string) (RGB, error) {
	if len(s) != len("#RRGGBB") || s[0] != '#' {
		return RGB{}, fmt.Errorf("colour %q is not #RRGGBB", s)
	}
	n, err := strconv.ParseUint(s[1:], 16, 24)
	if err != nil {
		return RGB{}, fmt.Errorf("colour %q is not #RRGGBB", s)
	}
	return RGB{uint8(n >> 16), uint8(n >> 8), uint8(n)}, nil //nolint:gosec // n has 24 bits
}

func (c RGB) String() string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

// ANSI 16-colour indices used by the default theme.
const (
	ansiRed          = 1
	ansiGreen        = 2
	ansiYellow       = 3
	ansiBlue         = 4
	ansiMagenta      = 5
	ansiBrightBlack  = 8
	ansiDefaultFaint = -1 // default foreground with the faint attribute
)

// Value is a token's colour on one background at every colour depth.
type Value struct {
	TrueColor RGB
	ANSI256   uint8
	// ANSI is a 16-colour index, or ansiDefaultFaint: many light themes
	// render ANSI 8 nearly invisible, so muted text uses faint instead.
	ANSI int
}

// Pair is a token's values for dark and light backgrounds.
type Pair struct {
	Dark, Light Value
}

// Default is the built-in theme. The 256-colour indices were picked by
// hand: nearest-colour mapping turned ok grey and accent purple on light
// backgrounds, and warn and error into near-identical oranges on dark.
var Default = [numTokens]Pair{
	Accent: {Dark: Value{RGB{0x7C, 0x9C, 0xB4}, 110, ansiBlue}, Light: Value{RGB{0x3F, 0x67, 0x82}, 24, ansiBlue}},
	OK:     {Dark: Value{RGB{0x8F, 0xAF, 0x8A}, 108, ansiGreen}, Light: Value{RGB{0x4E, 0x74, 0x49}, 22, ansiGreen}},
	Warn:   {Dark: Value{RGB{0xD2, 0xB2, 0x6E}, 179, ansiYellow}, Light: Value{RGB{0x86, 0x65, 0x1F}, 94, ansiYellow}},
	Error:  {Dark: Value{RGB{0xC4, 0x7A, 0x6C}, 167, ansiRed}, Light: Value{RGB{0x9E, 0x4A, 0x3C}, 131, ansiRed}},
	Muted:  {Dark: Value{RGB{0x8A, 0x8F, 0x98}, 246, ansiDefaultFaint}, Light: Value{RGB{0x5F, 0x66, 0x70}, 241, ansiDefaultFaint}},
	Border: {Dark: Value{RGB{0x4B, 0x50, 0x59}, 239, ansiBrightBlack}, Light: Value{RGB{0xC8, 0xCC, 0xD2}, 252, ansiBrightBlack}},
	// Keys are a soft orchid (OKLCH hue 320, chroma 0.075): a hue no
	// other token or shipped theme uses, as light as the space between
	// muted and body text, saturated enough to notice and too little to
	// pull the eye. Measured in OKLCH against every shipped theme.
	Key: {Dark: Value{RGB{0xC0, 0x9C, 0xC9}, 182, ansiMagenta}, Light: Value{RGB{0x66, 0x43, 0x6E}, 96, ansiMagenta}},
}

// xterm's 256-colour palette: 16 system colours, a 6x6x6 cube, then 24
// greys. The system colours are set by the terminal theme and have no
// fixed value.
var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

const (
	cubeStart = 16
	greyStart = 232
	greyBase  = 8
	greyStep  = 10
	cubeSide  = 6
)

// ANSI256RGB returns the colour xterm uses for index i, or false for the 16
// system colours, whose values depend on the terminal's theme.
func ANSI256RGB(i uint8) (RGB, bool) {
	switch {
	case i < cubeStart:
		return RGB{}, false
	case i >= greyStart:
		v := greyBase + greyStep*(i-greyStart)
		return RGB{v, v, v}, true
	}
	n := i - cubeStart
	return RGB{
		cubeLevels[n/(cubeSide*cubeSide)],
		cubeLevels[n/cubeSide%cubeSide],
		cubeLevels[n%cubeSide],
	}, true
}

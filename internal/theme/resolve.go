// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Override replaces a token's colour from the user's config. A nil side
// keeps the theme's value for that background.
type Override struct {
	Dark, Light *RGB
}

// Theme is a named palette. A nil set keeps fstui's default values for that
// background, so a dark-only theme still reads well on a light terminal.
type Theme struct {
	Name   string
	Source string // where the palette comes from, with its license
	Dark   *Set
	Light  *Set
}

// Set is a theme's colours for one background.
type Set struct {
	// Background is the colour the palette was designed for, if the theme
	// names it. It is used only to check contrast; fstui never paints the
	// background.
	Background *RGB
	Colors     [numTokens]*RGB // nil keeps the default theme's colour
	Meter      *Gradient       // nil: ok, warn, error of this set
}

// Gradient colours meters by position, btop style: Start at the empty end,
// Mid at the warn threshold, End from the error threshold on.
type Gradient struct {
	Start, Mid, End RGB
}

// Palette is a theme resolved for one terminal: its background and its
// colour depth. Views take styles from it by token.
type Palette struct {
	styles  [numTokens]lipgloss.Style
	values  [numTokens]Value
	meter   Gradient
	profile colorprofile.Profile
}

// Style returns the style for a token.
func (p Palette) Style(t Token) lipgloss.Style { return p.styles[t] }

// Resolve builds the palette of theme t for a dark or light background at
// the given colour profile, with the user's overrides on top. Colours
// from themes and overrides get the nearest fixed 256-colour entry; their
// 16-colour values stay the default theme's, since those colours are
// whatever the terminal theme makes them.
//
// Without colour (NO_COLOR, ASCII profile) the muted token is still faint,
// so secondary text stays distinguishable. Without a terminal (NoTTY)
// every style is plain.
func Resolve(t Theme, dark bool, profile colorprofile.Profile, overrides map[Token]Override) Palette {
	set := t.Light
	if dark {
		set = t.Dark
	}
	p := Palette{profile: profile}
	for tok := range numTokens {
		v := Default[tok].Light
		if dark {
			v = Default[tok].Dark
		}
		if set != nil && set.Colors[tok] != nil {
			v.TrueColor, v.ANSI256 = *set.Colors[tok], Nearest256(*set.Colors[tok])
		}
		if o := overrides[tok]; dark && o.Dark != nil {
			v.TrueColor, v.ANSI256 = *o.Dark, Nearest256(*o.Dark)
		} else if !dark && o.Light != nil {
			v.TrueColor, v.ANSI256 = *o.Light, Nearest256(*o.Light)
		}
		p.values[tok], p.styles[tok] = v, style(v, profile)
	}
	p.meter = Gradient{Start: p.values[OK].TrueColor, Mid: p.values[Warn].TrueColor, End: p.values[Error].TrueColor}
	if set != nil && set.Meter != nil {
		p.meter = *set.Meter
	}
	return p
}

func style(v Value, profile colorprofile.Profile) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch profile {
	case colorprofile.TrueColor:
		c := v.TrueColor
		return s.Foreground(lipgloss.RGBColor{R: c.R, G: c.G, B: c.B})
	case colorprofile.ANSI256:
		return s.Foreground(lipgloss.ANSIColor(v.ANSI256))
	case colorprofile.ANSI:
		if v.ANSI == ansiDefaultFaint {
			return s.Faint(true)
		}
		// BasicColor renders as SGR 30-37/90-97, which 16-colour
		// terminals understand; ANSIColor would emit the 256-colour form.
		return s.Foreground(ansi.BasicColor(v.ANSI)) //nolint:gosec // 16-colour indices
	case colorprofile.ASCII:
		return s.Faint(v.ANSI == ansiDefaultFaint)
	case colorprofile.NoTTY, colorprofile.Unknown:
	}
	return s
}

// Meter returns the style for a meter cell at position pos (0 to 100)
// along a meter whose warn and error thresholds are warn and crit.
//
// With 256 colours or more the colour moves smoothly from the gradient's
// start to its mid colour at warn, and to its end colour at crit. With 16
// colours, as on the Linux console, it steps between the ok, warn and
// error colours at the same thresholds, so the meter still says where the
// thresholds are. Without colour it is plain.
func (p Palette) Meter(pos, warn, crit int) lipgloss.Style {
	switch p.profile {
	case colorprofile.TrueColor, colorprofile.ANSI256:
		c := p.meter.at(pos, warn, crit)
		if p.profile == colorprofile.ANSI256 {
			return lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(Nearest256(c)))
		}
		return lipgloss.NewStyle().Foreground(lipgloss.RGBColor{R: c.R, G: c.G, B: c.B})
	case colorprofile.ANSI:
		switch {
		case pos >= crit:
			return p.styles[Error]
		case pos >= warn:
			return p.styles[Warn]
		}
		return p.styles[OK]
	case colorprofile.ASCII, colorprofile.NoTTY, colorprofile.Unknown:
	}
	return lipgloss.NewStyle()
}

// Graph returns the style for a graph cell whose fill reaches pos (0 to 100) of
// the graph's height: a subtle gradient from a dim accent (halfway to the border
// colour) at the bottom to the accent at the top, so a moving graph is easier to
// follow. With 16 colours or none it is the accent style, flat.
func (p Palette) Graph(pos int) lipgloss.Style {
	pos = min(max(pos, 0), percent)
	switch p.profile {
	case colorprofile.TrueColor, colorprofile.ANSI256:
		dim := mix(p.values[Border].TrueColor, p.values[Accent].TrueColor, graphDim)
		c := mix(dim, p.values[Accent].TrueColor, float64(pos)/percent)
		if p.profile == colorprofile.ANSI256 {
			return lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(Nearest256(c)))
		}
		return lipgloss.NewStyle().Foreground(lipgloss.RGBColor{R: c.R, G: c.G, B: c.B})
	case colorprofile.ANSI, colorprofile.ASCII, colorprofile.NoTTY, colorprofile.Unknown:
	}
	return p.styles[Accent]
}

const (
	percent  = 100
	graphDim = 0.5 // how far from the border colour towards the accent the bottom starts
)

// at interpolates the gradient linearly in RGB, as btop does.
func (g Gradient) at(pos, warn, crit int) RGB {
	switch {
	case pos >= crit:
		return g.End
	case pos >= warn:
		return mix(g.Mid, g.End, float64(pos-warn)/float64(max(crit-warn, 1)))
	}
	return mix(g.Start, g.Mid, float64(pos)/float64(max(warn, 1)))
}

func mix(a, b RGB, t float64) RGB {
	ch := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return RGB{ch(a.R, b.R), ch(a.G, b.G), ch(a.B, b.B)}
}

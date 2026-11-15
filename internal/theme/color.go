// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import "math"

// The constants below are those of the sRGB standard (IEC 61966-2-1) and
// of CIE L*a*b* with the D65 white point.

// linear converts an sRGB channel to linear light. WCAG 2 quotes 0.03928
// for the threshold, which makes no difference at 8 bits.
func linear(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func lab(c RGB) (l, a, b float64) {
	r, g, bl := linear(c.R), linear(c.G), linear(c.B)
	x := (0.4124*r + 0.3576*g + 0.1805*bl) / 0.95047
	y := 0.2126*r + 0.7152*g + 0.0722*bl
	z := (0.0193*r + 0.1192*g + 0.9505*bl) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

func luminance(c RGB) float64 {
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// Contrast is the WCAG 2 contrast ratio between two colours, from 1 to 21.
func Contrast(a, b RGB) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// DeltaE is the CIE76 colour difference: about 2 is just noticeable,
// above 10 reads as a different colour at a glance.
func DeltaE(p, q RGB) float64 {
	l1, a1, b1 := lab(p)
	l2, a2, b2 := lab(q)
	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2))
}

// Nearest256 returns the xterm 256-colour index perceptually closest to c,
// from the fixed part of the palette: the 16 system colours depend on the
// terminal's theme and could be anything.
func Nearest256(c RGB) uint8 {
	best, bestDist := uint8(cubeStart), math.Inf(1)
	for i := cubeStart; i <= math.MaxUint8; i++ {
		p, _ := ANSI256RGB(uint8(i))
		if d := DeltaE(c, p); d < bestDist {
			best, bestDist = uint8(i), d
		}
	}
	return best
}

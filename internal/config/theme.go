// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// SystemThemes holds themes an administrator adds for every user.
const SystemThemes = "/etc/fstui/themes"

// minStateDeltaE: ok, warn and error closer than this look alike at a
// glance.
const minStateDeltaE = 10

// minThemeContrast: text tokens of a theme under this ratio against the
// background the theme was made for are reported as hard to read. Shipped
// themes keep their authors' colours and meet it; fstui's default theme
// meets 4.5:1.
const minThemeContrast = 2.5

// meterStops are a gradient's start, mid and end colours.
const meterStops = 3

// ThemeDirs returns where user themes are looked up, most specific first.
func ThemeDirs() []string {
	dirs := []string{SystemThemes}
	if f, err := UserFile(); err == nil {
		dirs = append([]string{filepath.Join(filepath.Dir(f), "themes")}, dirs...)
	}
	return dirs
}

// LoadTheme finds the theme called name in dirs, then among the themes
// shipped with fstui. A theme that cannot be found or read is reported,
// and the default theme is used.
func LoadTheme(name string, dirs ...string) (theme.Theme, []Problem) {
	return trust{euid: uint32(os.Geteuid()), top: "/"}.loadTheme(name, dirs) //nolint:gosec // uids are non-negative
}

func (t trust) loadTheme(name string, dirs []string) (theme.Theme, []Problem) {
	if name == theme.DefaultName {
		return theme.Theme{Name: name}, nil
	}
	for _, dir := range dirs {
		file := filepath.Join(dir, name+".yaml")
		data, err := t.read(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return theme.Theme{Name: theme.DefaultName}, []Problem{{File: file, Msg: fmt.Sprintf("ignored, using the default theme: %v", err)}}
		}
		return parseTheme(name, file, data)
	}
	if data, ok := theme.Builtin(name); ok {
		return parseTheme(name, "builtin theme "+name, data)
	}
	return theme.Theme{Name: theme.DefaultName}, []Problem{{File: "config", Msg: fmt.Sprintf(
		"theme %q not found in %v or among the shipped themes %v; using the default theme", name, dirs, theme.BuiltinNames(),
	)}}
}

// parseTheme reads a theme file. Bad entries are reported and skipped; the
// rest of the theme still applies.
func parseTheme(name, file string, data []byte) (theme.Theme, []Problem) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return theme.Theme{Name: theme.DefaultName}, []Problem{{File: file, Msg: fmt.Sprintf("ignored, not valid YAML: %v", err)}}
	}
	th := theme.Theme{Name: name}
	a := &applier{file: file}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return th, []Problem{{File: file, Msg: "ignored: a theme is a mapping with name, source, dark and light"}}
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		switch k.Value {
		case "name", "source":
			s, err := scalar(v, "!!str", "text")
			if err != nil {
				a.report(v, "%s: %v", k.Value, err)
			} else if k.Value == "source" {
				th.Source = s
			}
		case "dark":
			th.Dark = a.themeSet(v, "dark")
		case "light":
			th.Light = a.themeSet(v, "light")
		default:
			a.report(k, "unknown key %q (want name, source, dark, light)", k.Value)
		}
	}
	return th, append(append(a.problems, distinctStates(file, th)...), readable(file, th)...)
}

func (a *applier) themeSet(n *yaml.Node, side string) *theme.Set {
	if n.Kind != yaml.MappingNode {
		a.report(n, "%s: want a mapping of tokens to colours", side)
		return nil
	}
	s := &theme.Set{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch k.Value {
		case "background":
			if c, err := theme.ParseHex(v.Value); err != nil {
				a.report(v, "%s.background: %v", side, err)
			} else {
				s.Background = &c
			}
		case "meter":
			if g, err := gradient(v); err != nil {
				a.report(v, "%s.meter: %v", side, err)
			} else {
				s.Meter = g
			}
		default:
			tok, ok := theme.TokenByName(k.Value)
			if !ok {
				a.report(k, "%s: unknown token %q (tokens: accent, ok, warn, error, muted, border; also background, meter)", side, k.Value)
				continue
			}
			c, err := theme.ParseHex(v.Value)
			if err != nil {
				a.report(v, "%s.%s: %v", side, k.Value, err)
				continue
			}
			s.Colors[tok] = &c
		}
	}
	return s
}

func gradient(n *yaml.Node) (*theme.Gradient, error) {
	if n.Kind != yaml.SequenceNode || len(n.Content) != meterStops {
		return nil, errors.New(`want three colours: ["#start", "#mid", "#end"]`)
	}
	var c [meterStops]theme.RGB
	for i, v := range n.Content {
		rgb, err := theme.ParseHex(v.Value)
		if err != nil {
			return nil, err
		}
		c[i] = rgb
	}
	return &theme.Gradient{Start: c[0], Mid: c[1], End: c[2]}, nil
}

// distinctStates warns when two of ok, warn and error are perceptually
// close, at truecolor or at their nearest 256-colour entries: the failure
// measured with automatic colour mapping.
func distinctStates(file string, th theme.Theme) []Problem {
	var out []Problem
	for _, side := range []struct {
		name string
		set  *theme.Set
	}{{"dark", th.Dark}, {"light", th.Light}} {
		if side.set == nil {
			continue
		}
		states := []theme.Token{theme.OK, theme.Warn, theme.Error}
		for i, a := range states {
			for _, b := range states[i+1:] {
				ca, cb := side.set.Colors[a], side.set.Colors[b]
				if ca == nil || cb == nil {
					continue
				}
				p256a, _ := theme.ANSI256RGB(theme.Nearest256(*ca))
				p256b, _ := theme.ANSI256RGB(theme.Nearest256(*cb))
				if d := min(theme.DeltaE(*ca, *cb), theme.DeltaE(p256a, p256b)); d < minStateDeltaE {
					out = append(out, Problem{File: file, Msg: fmt.Sprintf(
						"%s: %s and %s are hard to tell apart (colour distance %.1f, want at least %d)", side.name, a, b, d, minStateDeltaE,
					)})
				}
			}
		}
	}
	return out
}

// readable warns about text tokens that are hard to read on the theme's
// own background. Border is exempt: it is meant to be faint.
func readable(file string, th theme.Theme) []Problem {
	var out []Problem
	for _, side := range []struct {
		name string
		set  *theme.Set
	}{{"dark", th.Dark}, {"light", th.Light}} {
		if side.set == nil || side.set.Background == nil {
			continue
		}
		for _, tok := range []theme.Token{theme.Accent, theme.OK, theme.Warn, theme.Error, theme.Muted} {
			c := side.set.Colors[tok]
			if c == nil {
				continue
			}
			if r := theme.Contrast(*c, *side.set.Background); r < minThemeContrast {
				out = append(out, Problem{File: file, Msg: fmt.Sprintf(
					"%s.%s %v is hard to read on %v (contrast %.1f:1, want at least %.1f:1)", side.name, tok, *c, *side.set.Background, r, minThemeContrast,
				)})
			}
		}
	}
	return out
}

// ThemeNames lists the shipped themes and the theme files in dirs, each
// name once, shipped ones first.
func ThemeNames(dirs ...string) []string {
	names := theme.BuiltinNames()
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil {
			continue // a malformed pattern cannot happen: dir is joined literally
		}
		for _, f := range files {
			// Only names config.yaml accepts; anything else cannot be
			// selected and may hold characters unfit for display.
			if n := strings.TrimSuffix(filepath.Base(f), ".yaml"); themeName.MatchString(n) && !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	return names
}

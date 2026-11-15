// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// Value ranges. Refresh minimums keep a typo from polling zpool status
// many times a second; maximums keep a typo from freezing a view.
const (
	percentMax      = 100
	scrubDaysMax    = 3650
	refreshMaxSec   = 3600
	refreshPoolsMin = 2
	refreshDSMin    = 5
	refreshARCMin   = 1
)

// themeName keeps theme names usable as file names under themes/.
var themeName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// setter validates one value node and stores it in c.
type setter func(a *applier, c *Config, n *yaml.Node) error

// applier applies one file's document to a Config and collects problems.
type applier struct {
	file     string
	problems []Problem
}

func (a *applier) report(n *yaml.Node, format string, args ...any) {
	a.problems = append(a.problems, Problem{File: a.file, Line: n.Line, Msg: fmt.Sprintf(format, args...)})
}

var topLevel = map[string]setter{
	"theme":     setTheme,
	"colors":    setColors,
	"units":     setUnits,
	"ascii":     setASCII,
	"mouse":     func(_ *applier, c *Config, n *yaml.Node) error { return boolean(n, &c.Mouse) },
	"superuser": func(_ *applier, c *Config, n *yaml.Node) error { return boolean(n, &c.Superuser) },
	"natural_scrolling": func(_ *applier, c *Config, n *yaml.Node) error {
		return boolean(n, &c.NaturalScrolling)
	},
	"thresholds": nested("thresholds", thresholdKeys),
	"refresh":    nested("refresh", refreshKeys),
	"keys":       setKeys,
	"events":     nested("events", eventKeys),
	"dashboard":  nested("dashboard", dashboardKeys),
}

var thresholdKeys = map[string]setter{
	"capacity_warn":      positive(func(c *Config) *int { return &c.Thresholds.CapacityWarn }, percentMax),
	"capacity_error":     positive(func(c *Config) *int { return &c.Thresholds.CapacityError }, percentMax),
	"fragmentation_warn": positive(func(c *Config) *int { return &c.Thresholds.FragmentationWarn }, percentMax),
	"scrub_overdue_days": positive(func(c *Config) *int { return &c.Thresholds.ScrubOverdueDays }, scrubDaysMax),
}

var refreshKeys = map[string]setter{
	"pools":    seconds(func(c *Config) *time.Duration { return &c.Refresh.Pools }, refreshPoolsMin),
	"datasets": seconds(func(c *Config) *time.Duration { return &c.Refresh.Datasets }, refreshDSMin),
	"arc":      seconds(func(c *Config) *time.Duration { return &c.Refresh.ARC }, refreshARCMin),
}

// mapping applies every key of a mapping node. Unknown keys and invalid
// values are reported and skipped; the remaining keys still apply.
func (a *applier) mapping(c *Config, n *yaml.Node, keys map[string]setter, path string) {
	if n.Kind != yaml.MappingNode {
		a.report(n, "%s must be a mapping of keys to values", describe(path))
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		full := k.Value
		if path != "" {
			full = path + "." + k.Value
		}
		set, ok := keys[k.Value]
		if !ok {
			a.report(k, "unknown setting %q", full)
			continue
		}
		if v.Kind == yaml.AliasNode {
			a.report(v, "%s: anchors and aliases are not supported", full)
			continue
		}
		if err := set(a, c, v); err != nil {
			a.report(v, "%s: %v", full, err)
		} else if v.Kind != yaml.MappingNode {
			c.Sources[full] = a.file
		}
	}
}

func describe(path string) string {
	if path == "" {
		return "the file"
	}
	return path
}

func nested(name string, keys map[string]setter) setter {
	return func(a *applier, c *Config, n *yaml.Node) error {
		a.mapping(c, n, keys, name)
		return nil
	}
}

func scalar(n *yaml.Node, tag, want string) (string, error) {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != tag {
		return "", fmt.Errorf("want %s, found %q", want, n.Value)
	}
	return n.Value, nil
}

func boolean(n *yaml.Node, dst *bool) error {
	s, err := scalar(n, "!!bool", "true or false")
	if err != nil {
		return err
	}
	*dst = s == "true"
	return nil
}

// positive sets a whole number from 1 to hi.
func positive(field func(*Config) *int, hi int) setter {
	return func(_ *applier, c *Config, n *yaml.Node) error {
		v, err := intIn(n, 1, hi)
		if err != nil {
			return err
		}
		*field(c) = v
		return nil
	}
}

func seconds(field func(*Config) *time.Duration, lo int) setter {
	return func(_ *applier, c *Config, n *yaml.Node) error {
		v, err := intIn(n, lo, refreshMaxSec)
		if err != nil {
			return err
		}
		*field(c) = time.Duration(v) * time.Second
		return nil
	}
}

func intIn(n *yaml.Node, lo, hi int) (int, error) {
	want := fmt.Sprintf("a whole number from %d to %d", lo, hi)
	s, err := scalar(n, "!!int", want)
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < lo || v > hi {
		return 0, fmt.Errorf("want %s, found %q", want, s)
	}
	return v, nil
}

func oneOf(n *yaml.Node, values ...string) (string, error) {
	if n.Kind == yaml.ScalarNode {
		for _, v := range values {
			if n.Value == v {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("want one of %q, found %q", values, n.Value)
}

func setTheme(_ *applier, c *Config, n *yaml.Node) error {
	s, err := scalar(n, "!!str", "a theme name")
	if err != nil {
		return err
	}
	if !themeName.MatchString(s) {
		return fmt.Errorf("theme name %q may contain only a-z, 0-9, - and _", s)
	}
	c.Theme = s
	return nil
}

func setUnits(_ *applier, c *Config, n *yaml.Node) error {
	s, err := oneOf(n, "iec", "exact")
	if err != nil {
		return err
	}
	c.Units = map[string]Units{"iec": IEC, "exact": Exact}[s]
	return nil
}

func setASCII(_ *applier, c *Config, n *yaml.Node) error {
	s, err := oneOf(n, "auto", "true", "false")
	if err != nil {
		return err
	}
	c.ASCII = map[string]ASCII{"auto": ASCIIAuto, "true": ASCIIOn, "false": ASCIIOff}[s]
	return nil
}

// setKeys reads action rebindings: each action takes one key or a list,
// e.g. "select: enter" or "left: [h, left]".
func setKeys(a *applier, c *Config, n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("want a mapping of action names to keys")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		var ks []string
		switch v.Kind {
		case yaml.ScalarNode:
			ks = []string{v.Value}
		case yaml.SequenceNode:
			for _, e := range v.Content {
				if e.Kind != yaml.ScalarNode || e.Value == "" {
					a.report(e, "keys.%s: want key names such as j, enter or ctrl+d", k.Value)
					ks = nil
					break
				}
				ks = append(ks, e.Value)
			}
		case yaml.MappingNode, yaml.AliasNode, yaml.DocumentNode:
			a.report(v, "keys.%s: want a key or a list of keys", k.Value)
		}
		if ks != nil {
			c.Keys[k.Value] = ks
		}
	}
	return nil
}

// setColors reads per-token overrides: either one colour for both
// backgrounds, or a mapping with dark and light.
func setColors(a *applier, c *Config, n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("want a mapping of token names to colours")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		tok, ok := theme.TokenByName(k.Value)
		if !ok {
			a.report(k, "colors: unknown token %q (tokens: accent, ok, warn, error, muted, border)", k.Value)
			continue
		}
		col, err := color(v, c.Colors[tok])
		if err != nil {
			a.report(v, "colors.%s: %v", k.Value, err)
			continue
		}
		c.Colors[tok] = col
	}
	return nil
}

// color parses one token override on top of prev, so a later file can
// change only the light value.
func color(n *yaml.Node, prev theme.Override) (theme.Override, error) {
	if n.Kind == yaml.ScalarNode {
		rgb, err := theme.ParseHex(n.Value)
		if err != nil {
			return theme.Override{}, err
		}
		return theme.Override{Dark: &rgb, Light: &rgb}, nil
	}
	if n.Kind != yaml.MappingNode {
		return theme.Override{}, fmt.Errorf(`want "#RRGGBB" or { dark: "#RRGGBB", light: "#RRGGBB" }`)
	}
	out := prev
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		rgb, err := theme.ParseHex(v.Value)
		if err != nil {
			return theme.Override{}, err
		}
		switch k.Value {
		case "dark":
			out.Dark = &rgb
		case "light":
			out.Light = &rgb
		default:
			return theme.Override{}, fmt.Errorf("unknown key %q (want dark or light)", k.Value)
		}
	}
	return out, nil
}

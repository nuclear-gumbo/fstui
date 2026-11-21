// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Kind is the type of a setting's value.
type Kind int

// Kinds.
const (
	KindInt      Kind = iota // a whole number in [Min, Max]
	KindSeconds              // whole seconds in [Min, Max]
	KindDuration             // a Go duration such as 30s or 24h, or 0, in [MinDur, MaxDur]
	KindFloat                // a number in [MinFloat, MaxFloat]
	KindBool                 // true or false
	KindChoice               // one of Choices
	KindString               // text matching Pattern
)

// Spec is one setting with a single value: everything fstui knows about
// it, in one place. The parser, the Config view, Value and the
// documentation test all read the registry, so a setting declared here
// can be set in config.yaml and its drop-ins, edited in the Config view,
// and must be documented in docs/configuration.md.
type Spec struct {
	Path     string // dotted path in config.yaml, e.g. "thresholds.capacity_warn"
	Desc     string // one line for the view
	Kind     Kind
	Min, Max int           // KindInt, KindSeconds
	MinDur   time.Duration // KindDuration
	MaxDur   time.Duration
	MinFloat float64 // KindFloat
	MaxFloat float64
	Choices  []string       // KindChoice; nil for a theme, whose names are found at run time
	Pattern  *regexp.Regexp // KindString
	Want     string         // KindString: the form, in words
	// Restart marks a setting fstui reads once, when it starts: a saved
	// change applies the next time.
	Restart bool
	get     func(*Config) string
	set     func(*Config, string) error // the value has passed Check
}

// Check validates a value as written in the file or typed in the view.
func (s Spec) Check(v string) error {
	switch s.Kind {
	case KindInt, KindSeconds:
		n, err := strconv.Atoi(v)
		if err != nil || n < s.Min || n > s.Max {
			return fmt.Errorf("want a whole number from %d to %d, found %q", s.Min, s.Max, v)
		}
	case KindDuration:
		d, err := parseDuration(v)
		if err != nil || d < s.MinDur || d > s.MaxDur {
			return fmt.Errorf("want a duration from %s to %s, such as 30s, 5m or 24h, found %q", fmtDuration(s.MinDur), fmtDuration(s.MaxDur), v)
		}
	case KindFloat:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < s.MinFloat || f > s.MaxFloat {
			return fmt.Errorf("want a number from %g to %g, found %q", s.MinFloat, s.MaxFloat, v)
		}
	case KindBool:
		if v != "true" && v != "false" {
			return fmt.Errorf("want true or false, found %q", v)
		}
	case KindChoice:
		if s.Choices != nil && !slices.Contains(s.Choices, v) {
			return fmt.Errorf("want one of %q, found %q", s.Choices, v)
		}
	case KindString:
		if !s.Pattern.MatchString(v) {
			return fmt.Errorf("want %s, found %q", s.Want, v)
		}
	}
	return nil
}

// Help is the accepted values in words, for the view.
func (s Spec) Help() string {
	switch s.Kind {
	case KindInt:
		return fmt.Sprintf("A whole number from %d to %d", s.Min, s.Max)
	case KindSeconds:
		return fmt.Sprintf("Seconds, from %d to %d", s.Min, s.Max)
	case KindDuration:
		return fmt.Sprintf("A duration from %s to %s, such as 30s, 5m or 24h", fmtDuration(s.MinDur), fmtDuration(s.MaxDur))
	case KindFloat:
		return fmt.Sprintf("A number from %g to %g", s.MinFloat, s.MaxFloat)
	case KindBool:
		return "true or false"
	case KindString:
		return "Text: " + s.Want
	case KindChoice:
	}
	return "Values: " + strings.Join(s.Choices, ", ")
}

// Typed reports whether the value is typed in the view rather than
// stepped through.
func (s Spec) Typed() bool {
	return s.Kind == KindInt || s.Kind == KindSeconds || s.Kind == KindDuration || s.Kind == KindFloat || s.Kind == KindString
}

// options are the values the view steps through.
func (s Spec) options() []string {
	if s.Kind == KindBool {
		return []string{"true", "false"}
	}
	return s.Choices
}

// Options are the values the view steps through, for a stepped setting.
func (s Spec) Options() []string { return s.options() }

// fromNode reads the value of n as this setting's kind writes it.
func (s Spec) fromNode(n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("want a single value, found a %s", map[yaml.Kind]string{yaml.MappingNode: "mapping", yaml.SequenceNode: "list"}[n.Kind])
	}
	tag := n.ShortTag()
	switch {
	case (s.Kind == KindInt || s.Kind == KindSeconds) && tag != "!!int",
		s.Kind == KindBool && tag != "!!bool",
		s.Kind == KindFloat && tag != "!!float" && tag != "!!int":
		return "", fmt.Errorf("want %s, found %q", strings.ToLower(s.Help()[:1])+s.Help()[1:], n.Value)
	}
	return n.Value, s.Check(n.Value)
}

func (s Spec) apply(_ *applier, c *Config, n *yaml.Node) error {
	v, err := s.fromNode(n)
	if err != nil {
		return err
	}
	return s.set(c, v)
}

// Value returns a setting's current value as it is written in the file.
func (c Config) Value(path string) string {
	if s, ok := specByPath[path]; ok {
		return s.get(&c)
	}
	return ""
}

// SpecFor is the setting at path.
func SpecFor(path string) (Spec, bool) {
	s, ok := specByPath[path]
	return s, ok
}

// Settings lists every setting the Config view edits, in the registry's
// order; themes are the theme names to step through.
func Settings(themes []string) []Spec {
	out := slices.Clone(registry)
	for i := range out {
		if out[i].Path == "theme" {
			out[i].Choices = themes
		}
	}
	return out
}

// Builders for the registry.

func intSpec(path, desc string, lo, hi int, f func(*Config) *int) Spec {
	return Spec{
		Path: path, Desc: desc, Kind: KindInt, Min: lo, Max: hi,
		get: func(c *Config) string { return strconv.Itoa(*f(c)) },
		set: func(c *Config, v string) error { *f(c), _ = strconv.Atoi(v); return nil },
	}
}

// secondsSpec is a refresh interval in seconds, from lo to an hour.
func secondsSpec(path, desc string, lo int, f func(*Config) *time.Duration) Spec {
	return Spec{
		Path: path, Desc: desc, Kind: KindSeconds, Min: lo, Max: refreshMaxSec,
		get: func(c *Config) string { return strconv.Itoa(int(f(c).Seconds())) },
		set: func(c *Config, v string) error {
			n, _ := strconv.Atoi(v)
			*f(c) = time.Duration(n) * time.Second
			return nil
		},
	}
}

func durationSpec(path, desc string, lo, hi time.Duration, f func(*Config) *time.Duration) Spec {
	return Spec{
		Path: path, Desc: desc, Kind: KindDuration, MinDur: lo, MaxDur: hi,
		get: func(c *Config) string { return fmtDuration(*f(c)) },
		set: func(c *Config, v string) error { d, err := parseDuration(v); *f(c) = d; return err },
	}
}

func floatSpec(path, desc string, lo, hi float64, f func(*Config) *float64) Spec {
	return Spec{
		Path: path, Desc: desc, Kind: KindFloat, MinFloat: lo, MaxFloat: hi,
		get: func(c *Config) string { return strconv.FormatFloat(*f(c), 'g', -1, 64) },
		set: func(c *Config, v string) error { *f(c), _ = strconv.ParseFloat(v, 64); return nil },
	}
}

func stringSpec(path, desc, want string, pattern *regexp.Regexp, f func(*Config) *string) Spec {
	return Spec{
		Path: path, Desc: desc, Kind: KindString, Pattern: pattern, Want: want,
		get: func(c *Config) string { return *f(c) },
		set: func(c *Config, v string) error { *f(c) = v; return nil },
	}
}

func boolSpec(path, desc string, f func(*Config) *bool) Spec {
	return Spec{
		Path: path, Desc: desc, Kind: KindBool,
		get: func(c *Config) string { return strconv.FormatBool(*f(c)) },
		set: func(c *Config, v string) error { *f(c) = v == "true"; return nil },
	}
}

func choiceSpec(path, desc string, choices []string, f func(*Config) *string) Spec {
	return Spec{
		Path: path, Desc: desc, Kind: KindChoice, Choices: choices,
		get: func(c *Config) string { return *f(c) },
		set: func(c *Config, v string) error { *f(c) = v; return nil },
	}
}

// parseDuration reads a Go duration such as 30s or 24h, or 0.
func parseDuration(v string) (time.Duration, error) {
	if v == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration", v)
	}
	return d, nil
}

// fmtDuration writes a duration as config.yaml would: 30s, 5m, 24h, 0.
func fmtDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d%time.Second == 0:
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	return d.String()
}

// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads fstui's settings from YAML files: built-in defaults,
// then /etc/fstui/config.yaml, then the user's config.yaml, each overriding
// the previous key by key.
//
// A bad value never stops fstui. It is reported as a Problem with file and
// line, the setting keeps its previous value, and the rest of the file
// still applies. Files that another user could have written are not read
// at all, because fstui usually runs as root (load.go).
package config

import (
	"fmt"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// Units selects how sizes are shown.
type Units int

// Units.
const (
	IEC   Units = iota + 1 // 3.2 GiB
	Exact                  // 3 435 973 837
)

// ASCII selects box-drawing and block characters or a plain ASCII fallback.
type ASCII int

// ASCII modes.
const (
	ASCIIAuto ASCII = iota + 1 // ASCII on the Linux console, Unicode elsewhere
	ASCIIOn
	ASCIIOff
)

// Thresholds decide when a value is shown as a warning.
type Thresholds struct {
	CapacityWarn      int // percent
	CapacityError     int // percent
	FragmentationWarn int // percent
	ScrubOverdueDays  int
}

// Refresh holds polling intervals.
type Refresh struct {
	Pools    time.Duration
	Datasets time.Duration
	ARC      time.Duration
}

// Config is the complete set of settings.
type Config struct {
	Theme  string // name of a theme file
	Colors map[theme.Token]theme.Override
	Units  Units
	ASCII  ASCII
	Mouse  bool
	// NaturalScrolling reverses the wheel: content follows the fingers,
	// as on touchpads set that way.
	NaturalScrolling bool
	Superuser        bool // hide advisory warnings; never confirmations
	Thresholds       Thresholds
	Refresh          Refresh
	Events           Events
	Dashboard        Dashboard
	// Keys rebinds actions: action name to the keys that trigger it.
	// internal/ui/keys checks the action names.
	Keys map[string][]string
	// Sources names the file each setting came from, by dotted path
	// ("thresholds.capacity_warn"); settings not in it are defaults.
	Sources map[string]string
}

// FlagSource is the source of a setting given on the command line; such
// settings cannot be edited in the Settings view for that session.
const FlagSource = "command line"

// Built-in defaults.
const (
	defaultTheme             = "default"
	defaultCapacityWarn      = 80
	defaultCapacityError     = 90
	defaultFragmentationWarn = 50
	defaultScrubOverdueDays  = 35
	defaultRefreshPools      = 5 * time.Second
	defaultRefreshDatasets   = 15 * time.Second
	defaultRefreshARC        = time.Second
)

// Default returns the built-in settings.
func Default() Config {
	return Config{
		Theme:   defaultTheme,
		Colors:  map[theme.Token]theme.Override{},
		Keys:    map[string][]string{},
		Sources: map[string]string{},
		Units:   IEC,
		// On by default: tabs and rows are clickable and the wheel scrolls, as
		// in htop.
		Mouse: true,
		ASCII: ASCIIAuto,
		Thresholds: Thresholds{
			CapacityWarn:      defaultCapacityWarn,
			CapacityError:     defaultCapacityError,
			FragmentationWarn: defaultFragmentationWarn,
			ScrubOverdueDays:  defaultScrubOverdueDays,
		},
		Refresh: Refresh{
			Pools:    defaultRefreshPools,
			Datasets: defaultRefreshDatasets,
			ARC:      defaultRefreshARC,
		},
		Events:    Events{Retention: defaultRetention},
		Dashboard: defaultDashboard(),
	}
}

// Problem is something wrong in a config file, reported to the user.
type Problem struct {
	File string
	Line int // 0 when the problem concerns the whole file
	Msg  string
}

func (p Problem) String() string {
	if p.Line == 0 {
		return fmt.Sprintf("%s: %s", p.File, p.Msg)
	}
	return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Msg)
}

// check enforces rules that span several keys, after every file has been
// applied, so a user file may fix what the system file set.
func (c *Config) check() []Problem {
	t := &c.Thresholds
	if t.CapacityWarn >= t.CapacityError {
		p := Problem{File: "config", Msg: fmt.Sprintf(
			"thresholds.capacity_warn (%d) must be below capacity_error (%d); using %d and %d",
			t.CapacityWarn, t.CapacityError, defaultCapacityWarn, defaultCapacityError,
		)}
		t.CapacityWarn, t.CapacityError = defaultCapacityWarn, defaultCapacityError
		return []Problem{p}
	}
	return nil
}

// UseASCII resolves the ascii setting for a terminal: auto means plain
// characters on the Linux console (TERM=linux), whose font lacks most
// box-drawing and block glyphs.
func (c Config) UseASCII(term string) bool {
	if c.ASCII == ASCIIAuto {
		return term == "linux"
	}
	return c.ASCII == ASCIIOn
}

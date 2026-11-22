// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads fstui's settings from YAML files: built-in defaults,
// then /etc/fstui/config.yaml and the drop-ins in /etc/fstui/config.d,
// then the user's config.yaml and its config.d, each overriding the ones
// before it key by key (Files). Every setting is declared once in
// settings.go; the parser, the Config view and the reference in
// docs/configuration.md are made from that list.
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
	CheckpointAge     time.Duration // a checkpoint held longer is a problem
}

// Refresh holds polling intervals.
type Refresh struct {
	Pools         time.Duration
	Datasets      time.Duration
	ARC           time.Duration
	ARCHidden     time.Duration // the ARC while its view is not shown (the dashboard's graph)
	DatasetDetail time.Duration // a dataset's properties, snapshots and permissions
	Properties    time.Duration // a pool's properties and features
	Schedules     time.Duration // scrub timers and cron jobs
	Events        time.Duration // kernel events while the Events view is shown
	EventsHidden  time.Duration // and while it is not (the dashboard's panel)
	Tune          time.Duration // the Tune tab's measurements
}

// Charts are how much time the graphs show and keep.
type Charts struct {
	ARCWindow time.Duration
	IOWindow  time.Duration // the I/O view's rates
	IOKeep    time.Duration // the I/O history kept per pool and device
}

// Limits bound reads of large or slow output.
type Limits struct {
	ReadTimeout    time.Duration // a zfs or zpool read
	ListTimeout    time.Duration // listings of datasets and snapshots, which grow with them
	SlowReadFactor int           // after a read of d, wait at least this many d before the next
	HistoryEntries int           // of each kind kept from zpool history
	DiffEntries    int           // changes shown from zfs diff
	HistoryTimeout time.Duration // zpool history, which grows with the pool's age
	AuditEntries   int           // audit records the Audit tab reads
}

// UI holds settings of the interface itself.
type UI struct {
	StartView   string // the view fstui opens on; --dashboard overrides
	CursorBlink bool
	SplitFrom   int // terminal width from which a list and its detail sit side by side
	TextWidth   int // the widest a line of text runs in the Config view before it wraps
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
	Notifications    Notifications
	Dashboard        Dashboard
	Charts           Charts
	Limits           Limits
	UI               UI
	Tune             Tune
	Schedules        Schedules
	// Hosts are the other machines fstui can read over SSH; Remote is
	// how it connects to them.
	Hosts  []Host
	Remote Remote
	// Keys rebinds actions: action name to the keys that trigger it.
	// internal/ui/keys checks the action names.
	Keys map[string][]string
	// Sources names the file each setting came from, by dotted path
	// ("thresholds.capacity_warn"); settings not in it are defaults.
	Sources map[string]string
}

// FlagSource is the source of a setting given on the command line; such
// settings cannot be edited in the Config view for that session.
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

// Views are the view names ui.start_view takes, in view-number order.
var Views = []string{"dashboard", "pools", "datasets", "snapshots", "volumes", "arc", "io", "events", "plans", "config"}

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
			CheckpointAge:     24 * time.Hour,
		},
		Refresh: Refresh{
			Pools: defaultRefreshPools, Datasets: defaultRefreshDatasets, ARC: defaultRefreshARC,
			ARCHidden: 10 * time.Second, DatasetDetail: 30 * time.Second, Properties: time.Minute, Schedules: time.Minute,
			Events: 2 * time.Second, EventsHidden: 30 * time.Second, Tune: 5 * time.Second,
		},
		Charts: Charts{ARCWindow: 5 * time.Minute, IOWindow: 2 * time.Minute, IOKeep: 10 * time.Minute},
		Limits: Limits{
			ReadTimeout: 30 * time.Second, ListTimeout: 10 * time.Minute, SlowReadFactor: 4,
			HistoryEntries: 10000, DiffEntries: 20000, HistoryTimeout: 10 * time.Minute, AuditEntries: 2000,
		},
		UI:            UI{StartView: "pools", CursorBlink: true, SplitFrom: 120, TextWidth: 110},
		Tune:          defaultTune(),
		Schedules:     defaultSchedules(),
		Remote:        defaultRemote(),
		Events:        Events{Retention: defaultRetention},
		Notifications: defaultNotifications(),
		Dashboard:     defaultDashboard(),
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
	out := c.checkAlerts()
	d := Default()
	report := func(format string, args ...any) {
		out = append(out, Problem{File: "config", Msg: fmt.Sprintf(format, args...)})
	}
	if t := &c.Thresholds; t.CapacityWarn >= t.CapacityError {
		report("thresholds.capacity_warn (%d) must be below capacity_error (%d); using %d and %d",
			t.CapacityWarn, t.CapacityError, defaultCapacityWarn, defaultCapacityError)
		t.CapacityWarn, t.CapacityError = defaultCapacityWarn, defaultCapacityError
	}
	if ch := &c.Charts; ch.IOKeep < ch.IOWindow {
		report("charts.io_keep (%s) must be at least charts.io_window (%s); using %s and %s",
			fmtDuration(ch.IOKeep), fmtDuration(ch.IOWindow), fmtDuration(d.Charts.IOKeep), fmtDuration(d.Charts.IOWindow))
		ch.IOKeep, ch.IOWindow = d.Charts.IOKeep, d.Charts.IOWindow
	}
	if db := &c.Dashboard; db.TwoColumnsFrom >= db.ThreeColumnsFrom {
		report("dashboard.two_columns_from (%d) must be below three_columns_from (%d); using %d and %d",
			db.TwoColumnsFrom, db.ThreeColumnsFrom, d.Dashboard.TwoColumnsFrom, d.Dashboard.ThreeColumnsFrom)
		db.TwoColumnsFrom, db.ThreeColumnsFrom = d.Dashboard.TwoColumnsFrom, d.Dashboard.ThreeColumnsFrom
	}
	if t := &c.Tune; t.AmplificationFits >= t.AmplificationMismatch {
		report("tune.amplification_fits (%g) must be below amplification_mismatch (%g); using %g and %g",
			t.AmplificationFits, t.AmplificationMismatch, d.Tune.AmplificationFits, d.Tune.AmplificationMismatch)
		t.AmplificationFits, t.AmplificationMismatch = d.Tune.AmplificationFits, d.Tune.AmplificationMismatch
	}
	return out
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

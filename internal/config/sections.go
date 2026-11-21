// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "strings"

// Section is a group of settings the Config view shows together, by the
// first part of their paths.
type Section struct {
	Title string
	Desc  string   // what the section covers, in a line
	Paths []string // path prefixes ("refresh."), or whole paths ("theme")
}

// Sections are the Config view's groups, in the order it lists them.
// Every setting is in exactly one; a test checks it.
var Sections = []Section{
	{
		"Display", "Theme, sizes, characters, mouse, and the view fstui opens on",
		[]string{"theme", "units", "ascii", "mouse", "natural_scrolling", "ui."},
	},
	{
		"Warnings", "When a pool's capacity, fragmentation, scrub or checkpoint is warned about",
		[]string{"thresholds.", "superuser"},
	},
	{"Refresh", "How often each view reads ZFS again", []string{"refresh."}},
	{"Charts", "How much time the ARC and I/O graphs show", []string{"charts."}},
	{"Dashboard", "When the dashboard uses two or three columns", []string{"dashboard."}},
	{"Events", "How many kernel events are kept", []string{"events."}},
	{"Limits", "Timeouts and how much of a long listing is kept", []string{"limits."}},
	{"Notifications", "How often messages repeat, how often the service checks, when sending gives up", []string{"notifications."}},
	{"Schedules", "When scheduled checks, scrubs and trims run", []string{"schedules."}},
	{"Tune", "How much the Tune tab measures before it answers, and its margins", []string{"tune."}},
}

// PlaceIn is where a setting comes within its section: the index of the
// path or prefix that names it.
func PlaceIn(path string) int {
	if i := SectionOf(path); i >= 0 {
		for k, p := range Sections[i].Paths {
			if path == p || strings.HasSuffix(p, ".") && strings.HasPrefix(path, p) {
				return k
			}
		}
	}
	return 0
}

// SectionOf is the section a setting belongs to.
func SectionOf(path string) int {
	for i, s := range Sections {
		for _, p := range s.Paths {
			if path == p || strings.HasSuffix(p, ".") && strings.HasPrefix(path, p) {
				return i
			}
		}
	}
	return -1
}

// labels are what the Config view calls each setting: a name a person
// would use, with its unit. The path is shown beside the selected one.
var labels = map[string]string{
	"theme":             "Colour theme",
	"units":             "Size units",
	"ascii":             "Plain characters only",
	"mouse":             "Mouse",
	"natural_scrolling": "Natural scrolling",
	"ui.start_view":     "Open on view",
	"ui.cursor_blink":   "Blinking cursor",
	"ui.split_from":     "Side-by-side detail from width",

	"thresholds.capacity_warn":      "Capacity warning at (%)",
	"thresholds.capacity_error":     "Capacity full at (%)",
	"thresholds.fragmentation_warn": "Fragmentation warning at (%)",
	"thresholds.scrub_overdue_days": "Scrub overdue after (days)",
	"thresholds.checkpoint_age":     "Checkpoint held too long after",
	"superuser":                     "Hide advisory warnings",

	"refresh.pools":          "Pools (seconds)",
	"refresh.datasets":       "Datasets and volumes (seconds)",
	"refresh.arc":            "ARC, while shown (seconds)",
	"refresh.arc_hidden":     "ARC, in the background (seconds)",
	"refresh.dataset_detail": "A dataset's detail (seconds)",
	"refresh.properties":     "A pool's properties (seconds)",
	"refresh.schedules":      "Scrub timers and cron (seconds)",
	"refresh.events":         "Kernel events, while shown (seconds)",
	"refresh.events_hidden":  "Kernel events, in the background (seconds)",
	"refresh.tune":           "Tune measurements (seconds)",

	"charts.arc_window": "ARC graphs span",
	"charts.io_window":  "I/O rates span",
	"charts.io_keep":    "I/O history kept",

	"dashboard.columns":            "Columns (0 by width)",
	"dashboard.two_columns_from":   "Two columns from width",
	"dashboard.three_columns_from": "Three columns from width",

	"events.retention": "Events kept",

	"limits.read_timeout":     "Read timeout",
	"limits.list_timeout":     "Listing timeout",
	"limits.history_timeout":  "Pool history timeout",
	"limits.slow_read_factor": "Pause after a slow read (times its length)",
	"limits.history_entries":  "Pool history entries kept, each kind",
	"limits.diff_entries":     "Changes shown by zfs diff",
	"limits.audit_entries":    "Audit records shown",

	"notifications.repeat":          "Repeat an open problem every",
	"notifications.resolved":        "Say when a problem clears",
	"notifications.events":          "Kernel events sent by",
	"notifications.events_min":      "Kernel events from severity",
	"notifications.host":            "This system's name in messages",
	"notifications.heartbeat_url":   "Heartbeat URL",
	"notifications.attempt_timeout": "One try at sending may take",
	"notifications.send_timeout":    "All tries of a message may take",
	"notifications.retries":         "Tries again after a failure",
	"notifications.check_timeout":   "One check may take",
	"notifications.watch_interval":  "The service checks pools every",
	"notifications.watch_events":    "The service looks for events every",

	"schedules.check_every":    "Scheduled fstui check every",
	"schedules.cron_time":      "Cron runs scrubs and trims at",
	"schedules.scrub_weekday":  "Weekly scrub on",
	"schedules.scrub_monthday": "Monthly scrub on day",
	"schedules.trim_weekday":   "Weekly trim on",
	"schedules.trim_monthday":  "Monthly trim on day",

	"tune.min_window":             "Measure at least",
	"tune.min_written_mib":        "Written before judging amplification (MiB)",
	"tune.min_requests":           "Requests before judging record size",
	"tune.min_moved_mib":          "Moved before judging record size (MiB)",
	"tune.min_lookups":            "ARC lookups before judging memory",
	"tune.min_operations":         "Device operations before judging limits",
	"tune.min_prefetch":           "Prefetched blocks before judging prefetch",
	"tune.compression_gib":        "Data before judging compression (GiB)",
	"tune.walk_files":             "Files read for compression, at most",
	"tune.walk_time":              "Time reading files, at most",
	"tune.slow_txg":               "Transaction group slow after",
	"tune.small_ratio":            "Records too large at (times the request)",
	"tune.large_ratio":            "Requests too large at (times the record)",
	"tune.l2_headers_mib":         "L2ARC headers worth judging (MiB)",
	"tune.amplification_fits":     "Amplification fits up to (times the layout)",
	"tune.amplification_mismatch": "Amplification mismatch from (times the layout)",
	"tune.ghost_share":            "Ghost hits pointing at more memory (share)",
	"tune.prefetch_use":           "Prefetch wasted below (share used)",
	"tune.metadata_miss":          "Metadata crowded out from (share missed)",
	"tune.l2_share":               "L2ARC not worth it below (share hit)",
	"tune.slow_device":            "Slow device at (times the others)",
	"tune.queueing":               "Queueing in ZFS at (times disk wait)",
	"tune.poor_ratio":             "Poor compression below (ratio)",
	"tune.incompressible":         "Incompressible file below (ratio)",
}

// Label is what the Config view calls a setting; its path when it has no
// label.
func (s Spec) Label() string {
	if l, ok := labels[s.Path]; ok {
		return l
	}
	return s.Path
}

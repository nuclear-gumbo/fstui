// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"regexp"
	"strings"
	"time"
)

// registry is every setting with a single value, in the order the Config
// view lists them. A new setting is added here and nowhere else; lists
// and mappings (colors, keys, filters, panels, channels) are in custom.
var registry = []Spec{
	{
		Path: "theme", Desc: "Colour theme", Kind: KindChoice,
		get: func(c *Config) string { return c.Theme },
		set: func(c *Config, v string) error {
			if !themeName.MatchString(v) {
				return errThemeName(v)
			}
			c.Theme = v
			return nil
		},
	},
	{
		Path: "units", Desc: "Sizes in IEC units or exact byte counts", Kind: KindChoice, Choices: []string{"iec", "exact"},
		get: func(c *Config) string { return map[Units]string{IEC: "iec", Exact: "exact"}[c.Units] },
		set: func(c *Config, v string) error { c.Units = map[string]Units{"iec": IEC, "exact": Exact}[v]; return nil },
	},
	{
		Path: "ascii", Desc: "Plain characters instead of box and block glyphs; auto on the Linux console", Kind: KindChoice, Choices: []string{"auto", "true", "false"},
		get: func(c *Config) string {
			return map[ASCII]string{ASCIIAuto: "auto", ASCIIOn: "true", ASCIIOff: "false"}[c.ASCII]
		},
		set: func(c *Config, v string) error {
			c.ASCII = map[string]ASCII{"auto": ASCIIAuto, "true": ASCIIOn, "false": ASCIIOff}[v]
			return nil
		},
	},
	boolSpec("mouse", "Clickable tabs and rows, wheel scrolling", func(c *Config) *bool { return &c.Mouse }),
	boolSpec("natural_scrolling", "The wheel moves the content, as on a touchpad set to natural scrolling", func(c *Config) *bool { return &c.NaturalScrolling }),
	boolSpec("superuser", "Hide advisory warnings; confirmations are always shown", func(c *Config) *bool { return &c.Superuser }),

	intSpec("thresholds.capacity_warn", "Capacity shown as WARN from this percentage", 1, percentMax, func(c *Config) *int { return &c.Thresholds.CapacityWarn }),
	intSpec("thresholds.capacity_error", "Capacity shown as FULL from this percentage", 1, percentMax, func(c *Config) *int { return &c.Thresholds.CapacityError }),
	intSpec("thresholds.fragmentation_warn", "Warn about free-space fragmentation from this percentage", 1, percentMax, func(c *Config) *int { return &c.Thresholds.FragmentationWarn }),
	intSpec("thresholds.scrub_overdue_days", "Warn when the last scrub is older than this many days", 1, scrubDaysMax, func(c *Config) *int { return &c.Thresholds.ScrubOverdueDays }),
	durationSpec("thresholds.checkpoint_age", "Warn about a checkpoint held longer than this", time.Hour, 365*day, func(c *Config) *time.Duration { return &c.Thresholds.CheckpointAge }),

	secondsSpec("refresh.pools", "Seconds between pool reads", refreshPoolsMin, func(c *Config) *time.Duration { return &c.Refresh.Pools }),
	secondsSpec("refresh.datasets", "Seconds between dataset reads", refreshDSMin, func(c *Config) *time.Duration { return &c.Refresh.Datasets }),
	secondsSpec("refresh.arc", "Seconds between ARC reads while the ARC view is shown", refreshARCMin, func(c *Config) *time.Duration { return &c.Refresh.ARC }),
	secondsSpec("refresh.arc_hidden", "Seconds between ARC reads while its view is not shown (the dashboard's graph)", 1, func(c *Config) *time.Duration { return &c.Refresh.ARCHidden }),
	secondsSpec("refresh.dataset_detail", "Seconds between reads of a dataset's properties, snapshots and permissions", refreshDSMin, func(c *Config) *time.Duration { return &c.Refresh.DatasetDetail }),
	secondsSpec("refresh.properties", "Seconds between reads of a pool's properties and features", refreshDSMin, func(c *Config) *time.Duration { return &c.Refresh.Properties }),
	secondsSpec("refresh.schedules", "Seconds between reads of scrub timers and cron jobs", refreshDSMin, func(c *Config) *time.Duration { return &c.Refresh.Schedules }),
	secondsSpec("refresh.events", "Seconds between reads of kernel events while the Events view is shown", 1, func(c *Config) *time.Duration { return &c.Refresh.Events }),
	secondsSpec("refresh.events_hidden", "Seconds between reads of kernel events while the Events view is not shown", 1, func(c *Config) *time.Duration { return &c.Refresh.EventsHidden }),
	secondsSpec("refresh.tune", "Seconds between the Tune tab's measurements; applies the next time the tab opens", 1, func(c *Config) *time.Duration { return &c.Refresh.Tune }),

	durationSpec("charts.arc_window", "Time the ARC graphs show", time.Minute, time.Hour, func(c *Config) *time.Duration { return &c.Charts.ARCWindow }),
	durationSpec("charts.io_window", "Time the I/O rates show", 30*time.Second, time.Hour, func(c *Config) *time.Duration { return &c.Charts.IOWindow }),
	durationSpec("charts.io_keep", "I/O history kept per pool and device; at least io_window", time.Minute, 6*time.Hour, func(c *Config) *time.Duration { return &c.Charts.IOKeep }),

	restart(durationSpec("limits.read_timeout", "Longest a zfs or zpool read may take", 5*time.Second, 10*time.Minute, func(c *Config) *time.Duration { return &c.Limits.ReadTimeout })),
	durationSpec("limits.list_timeout", "Longest a listing of datasets or snapshots may take", 30*time.Second, 2*time.Hour, func(c *Config) *time.Duration { return &c.Limits.ListTimeout }),
	intSpec("limits.slow_read_factor", "After a read of d seconds, wait at least this many times d before the next", 1, 20, func(c *Config) *int { return &c.Limits.SlowReadFactor }),
	restart(intSpec("limits.history_entries", "Pool history entries kept, of user commands and of internal events each", 100, 1000000, func(c *Config) *int { return &c.Limits.HistoryEntries })),
	restart(intSpec("limits.diff_entries", "Changes shown from zfs diff", 100, 1000000, func(c *Config) *int { return &c.Limits.DiffEntries })),
	restart(durationSpec("limits.history_timeout", "Longest zpool history may take", 30*time.Second, 2*time.Hour, func(c *Config) *time.Duration { return &c.Limits.HistoryTimeout })),
	intSpec("limits.audit_entries", "Audit records the Audit tab reads, newest first", 100, 100000, func(c *Config) *int { return &c.Limits.AuditEntries }),

	restart(choiceSpec("ui.start_view", "The view fstui opens on; --dashboard overrides", Views, func(c *Config) *string { return &c.UI.StartView })),
	boolSpec("ui.cursor_blink", "A typing cursor blinks", func(c *Config) *bool { return &c.UI.CursorBlink }),
	intSpec("ui.split_from", "Terminal width from which a list and the selected item's detail sit side by side", 80, 1000, func(c *Config) *int { return &c.UI.SplitFrom }),
	intSpec("ui.text_width", "The widest a line of text runs in the Config view before it wraps, so a wide terminal does not spread a sentence across it", 60, 1000, func(c *Config) *int { return &c.UI.TextWidth }),

	intSpec("events.retention", "Events kept in memory", retentionMin, retentionMax, func(c *Config) *int { return &c.Events.Retention }),
	intSpec("dashboard.columns", "Dashboard columns; 0 chooses by the terminal's width", 0, dashboardColumnsMax, func(c *Config) *int { return &c.Dashboard.Columns }),
	intSpec("dashboard.two_columns_from", "With columns 0, two columns from this terminal width", 80, 1000, func(c *Config) *int { return &c.Dashboard.TwoColumnsFrom }),
	intSpec("dashboard.three_columns_from", "With columns 0, three columns from this terminal width", 80, 2000, func(c *Config) *int { return &c.Dashboard.ThreeColumnsFrom }),

	durationSpec("notifications.repeat", "While a problem lasts, send it again after this long; 0 sends it once", 0, maxRepeat, func(c *Config) *time.Duration { return &c.Notifications.Repeat }),
	boolSpec("notifications.resolved", "Send a message when a problem goes away", func(c *Config) *bool { return &c.Notifications.Resolved }),
	choiceSpec("notifications.events", "Who sends ZFS kernel events, such as checksum errors: on, fstui; off, nobody; auto, ZED when it is set up to send them, else fstui", []string{"auto", "on", "off"}, func(c *Config) *string { return &c.Notifications.Events }),
	choiceSpec("notifications.events_min", "The least serious kernel event sent: info, notice, warn or error", SeverityNames, func(c *Config) *string { return &c.Notifications.EventsMin }),
	stringSpec("notifications.host", "What messages call this system; empty uses its host name", "up to 64 letters, digits, ., - and _", hostName, func(c *Config) *string { return &c.Notifications.Host }),
	stringSpec("notifications.heartbeat_url", "Requested after every check, so a service outside this host (Healthchecks, an Uptime Kuma push monitor) warns you when the checks stop, as when the host is down; empty sends none", "an https:// URL", heartbeatURL, func(c *Config) *string { return &c.Notifications.HeartbeatURL }),
	durationSpec("notifications.attempt_timeout", "A try at sending that takes longer is given up", time.Second, 5*time.Minute, func(c *Config) *time.Duration { return &c.Notifications.AttemptTimeout }),
	durationSpec("notifications.send_timeout", "All the tries of one message together take no longer than this", 5*time.Second, 30*time.Minute, func(c *Config) *time.Duration { return &c.Notifications.SendTimeout }),
	intSpec("notifications.retries", "How many more times a message is tried after a network or server error", 0, 10, func(c *Config) *int { return &c.Notifications.Retries }),
	durationSpec("notifications.check_timeout", "A check of the pools that takes longer is stopped and reported", 30*time.Second, time.Hour, func(c *Config) *time.Duration { return &c.Notifications.CheckTimeout }),
	durationSpec("notifications.watch_interval", "How often the notification service (fstui watch) checks every pool", 30*time.Second, 24*time.Hour, func(c *Config) *time.Duration { return &c.Notifications.WatchInterval }),
	durationSpec("notifications.watch_events", "How often the service looks for new kernel events between checks, so they go out at once; 0 only at checks", 0, time.Hour, func(c *Config) *time.Duration { return &c.Notifications.WatchEvents }),

	choiceSpec("schedules.check_every", "How often a schedule written by fstui runs fstui check", CheckEvery, func(c *Config) *string { return &c.Schedules.CheckEvery }),
	stringSpec("schedules.cron_time", "When cron starts scrubs and trims fstui schedules", "HH:MM, 24-hour", clockTime, func(c *Config) *string { return &c.Schedules.CronTime }),
	choiceSpec("schedules.scrub_weekday", "Day of a weekly scrub under cron", Weekdays, func(c *Config) *string { return &c.Schedules.ScrubWeekday }),
	intSpec("schedules.scrub_monthday", "Day of the month of a monthly scrub under cron", 1, 28, func(c *Config) *int { return &c.Schedules.ScrubMonthday }),
	choiceSpec("schedules.trim_weekday", "Day of a weekly trim under cron", Weekdays, func(c *Config) *string { return &c.Schedules.TrimWeekday }),
	intSpec("schedules.trim_monthday", "Day of the month of a monthly trim under cron", 1, 28, func(c *Config) *int { return &c.Schedules.TrimMonthday }),

	durationSpec("tune.min_window", "I/O measured before the amplification and saturation questions answer", 10*time.Second, time.Hour, func(c *Config) *time.Duration { return &c.Tune.MinWindow }),
	intSpec("tune.min_written_mib", "MiB written by applications before amplification is judged", 1, 1<<20, func(c *Config) *int { return &c.Tune.MinWrittenMiB }),
	intSpec("tune.min_requests", "Requests to a dataset before its record size is judged", 1, 1<<30, func(c *Config) *int { return &c.Tune.MinRequests }),
	intSpec("tune.min_moved_mib", "MiB read or written by a dataset before its record size is judged", 1, 1<<20, func(c *Config) *int { return &c.Tune.MinMovedMiB }),
	intSpec("tune.min_lookups", "ARC lookups before the memory question answers", 100, 1<<30, func(c *Config) *int { return &c.Tune.MinLookups }),
	intSpec("tune.min_operations", "Device operations before saturation is judged", 10, 1<<30, func(c *Config) *int { return &c.Tune.MinOperations }),
	intSpec("tune.min_prefetch", "Prefetched blocks before prefetch is judged", 1, 1<<30, func(c *Config) *int { return &c.Tune.MinPrefetch }),
	intSpec("tune.compression_gib", "GiB a dataset holds before its compression is judged", 0, 1<<20, func(c *Config) *int { return &c.Tune.CompressionGiB }),
	intSpec("tune.walk_files", "Files the per-file compression walk reads at most", 1000, 100000000, func(c *Config) *int { return &c.Tune.WalkFiles }),
	durationSpec("tune.walk_time", "Longest the per-file compression walk runs", 5*time.Second, time.Hour, func(c *Config) *time.Duration { return &c.Tune.WalkTime }),
	durationSpec("tune.slow_txg", "A transaction group syncing longer than this is slow", time.Second, 10*time.Minute, func(c *Config) *time.Duration { return &c.Tune.SlowTXG }),
	intSpec("tune.small_ratio", "Records this many times the average request are too large for it", 2, 1024, func(c *Config) *int { return &c.Tune.SmallRatio }),
	intSpec("tune.large_ratio", "Requests this many records long are too large for the records", 2, 1024, func(c *Config) *int { return &c.Tune.LargeRatio }),
	intSpec("tune.l2_headers_mib", "MiB of L2ARC headers worth weighing against its hits", 1, 1<<20, func(c *Config) *int { return &c.Tune.L2HeadersMiB }),
	floatSpec("tune.amplification_fits", "Writes within this many times what the layout explains fit", 1, 100, func(c *Config) *float64 { return &c.Tune.AmplificationFits }),
	floatSpec("tune.amplification_mismatch", "Writes past this many times what the layout explains are a mismatch", 1, 1000, func(c *Config) *float64 { return &c.Tune.AmplificationMismatch }),
	floatSpec("tune.ghost_share", "Share of ARC misses just evicted that points at a larger ARC", 0, 1, func(c *Config) *float64 { return &c.Tune.GhostShare }),
	floatSpec("tune.prefetch_use", "Share of prefetched blocks read below which prefetch is wasted", 0, 1, func(c *Config) *float64 { return &c.Tune.PrefetchUse }),
	floatSpec("tune.metadata_miss", "Share of metadata lookups missing that points at metadata crowded out", 0, 1, func(c *Config) *float64 { return &c.Tune.MetadataMiss }),
	floatSpec("tune.l2_share", "Share of L2ARC lookups hitting below which it costs more than it gives", 0, 1, func(c *Config) *float64 { return &c.Tune.L2Share }),
	floatSpec("tune.slow_device", "A device this many times slower than its siblings is worth a look", 1, 100, func(c *Config) *float64 { return &c.Tune.SlowDevice }),
	floatSpec("tune.queueing", "Total wait this many times the disk wait is queueing in ZFS", 1, 100, func(c *Config) *float64 { return &c.Tune.Queueing }),
	floatSpec("tune.poor_ratio", "An expensive compression earning less than this ratio is worth a look", 1, 10, func(c *Config) *float64 { return &c.Tune.PoorRatio }),
	floatSpec("tune.incompressible", "Files saving less than this ratio do not compress", 1, 10, func(c *Config) *float64 { return &c.Tune.Incompressible }),
}

// restart marks a setting as read only when fstui starts.
func restart(s Spec) Spec {
	s.Restart = true
	return s
}

// day is a duration in days, for the ranges.
const day = 24 * time.Hour

// clockTime is what schedules.cron_time takes.
var clockTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// heartbeatURL is what notifications.heartbeat_url takes: nothing, or an
// https URL without spaces.
var heartbeatURL = regexp.MustCompile(`^(https://[^\s]{1,500})?$`)

// hostName is what notifications.host takes.
var hostName = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)

// custom are the settings that hold a list or a mapping, each with its
// own reader.
var custom = map[string]setter{
	"colors":                  setColors,
	"keys":                    setKeys,
	"events.filters":          setFilters,
	"dashboard.panels":        setPanels,
	"notifications.channels":  setChannels,
	"notifications.templates": setTemplates,
	"notifications.alerts":    setAlerts,
}

var specByPath = func() map[string]Spec {
	m := map[string]Spec{}
	for _, s := range registry {
		m[s.Path] = s
	}
	return m
}()

// topLevel is the schema of config.yaml: the registry and custom, as
// nested mappings of setters.
var topLevel = func() map[string]setter {
	tree := map[string]any{}
	add := func(path string, set setter) {
		parts := strings.Split(path, ".")
		m := tree
		for _, p := range parts[:len(parts)-1] {
			sub, ok := m[p].(map[string]any)
			if !ok {
				sub = map[string]any{}
				m[p] = sub
			}
			m = sub
		}
		m[parts[len(parts)-1]] = set
	}
	for _, s := range registry {
		add(s.Path, s.apply)
	}
	for path, set := range custom {
		add(path, set)
	}
	return setters(tree, "")
}()

// setters turns a tree of paths into setters, a section into a nested
// mapping.
func setters(tree map[string]any, prefix string) map[string]setter {
	out := map[string]setter{}
	for k, v := range tree {
		switch v := v.(type) {
		case setter:
			out[k] = v
		case map[string]any:
			out[k] = nested(prefix+k, setters(v, prefix+k+"."))
		}
	}
	return out
}

// Paths are every setting's path, single values and lists alike, for the
// documentation test.
func Paths() []string {
	out := make([]string, 0, len(registry)+len(custom))
	for _, s := range registry {
		out = append(out, s.Path)
	}
	for p := range custom {
		out = append(out, p)
	}
	return out
}

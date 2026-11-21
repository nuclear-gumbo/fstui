// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"go.yaml.in/yaml/v3"
)

// Alerts decide what fstui check and watch send. A template is a
// condition on one measure of a pool (how full it is, how many devices
// are down, how long reads wait), a severity and the line it sends; an
// alert turns a template on for some pools or all, and some channels or
// all, overriding its threshold or text. fstui ships templates for the
// usual cases (BuiltinTemplates); notifications.templates adds the
// user's own, in the same form.

// Metric is a measure a backend reports for each pool, or for the
// system, that a template compares. Label names it in a sentence;
// Example is a value it might have, and Detail what the backend might
// add, for showing a template's message filled in.
type Metric struct {
	Name, Kind, Label, Desc string // Kind: percent, bytes, count, days, duration, ms, state, events
	Example, Detail         string
}

// Metrics are the measures templates can use. A backend reports what it
// can; a pool without a measure never matches a template on it.
var Metrics = []Metric{
	{"capacity", "percent", "space used", "how full the pool is, as a percentage", "85", ""},
	{"free", "bytes", "free space", "space left for writing", "80G", ""},
	{"fragmentation", "percent", "free space fragmentation", "how fragmented the free space is", "55", ""},
	{"health", "state", "state", "the pool's state, such as ONLINE or DEGRADED", "DEGRADED", ": 1 device OFFLINE"},
	{"devices_down", "count", "devices not online", "devices that are offline, faulted, unavailable or removed", "1", ": 1 device OFFLINE"},
	{"device_errors", "count", "device errors", "read, write and checksum errors counted on the pool's devices", "3", ""},
	{"data_errors", "count", "data errors", "files with permanent data errors", "2", ""},
	{"scrub_age", "days", "time since the last scrub", "days since the last scrub finished", "40", ""},
	{"resilvering", "count", "resilvering", "whether a resilver is running", "1", ", 40% done"},
	{"checkpoint_age", "duration", "checkpoint age", "how long the pool has held a checkpoint", "30h", ""},
	{"read_wait", "ms", "read wait", "how long reads wait on average, over one second", "64", ""},
	{"write_wait", "ms", "write wait", "how long writes wait on average, over one second", "64", ""},
	{"versions_differ", "count", "version mismatch", "whether the zfs commands and the kernel module are different versions", "1", " (userland 2.3.9, kernel module 2.3.8)"},
	{"events", "events", "kernel events", "kernel events at or above a severity: info, notice, warn or error", "error", "tank: checksum error (sda); ereport.fs.zfs.checksum"},
}

// MetricNamed returns a metric by name.
func MetricNamed(name string) (Metric, bool) {
	i := slices.IndexFunc(Metrics, func(m Metric) bool { return m.Name == name })
	if i < 0 {
		return Metric{}, false
	}
	return Metrics[i], true
}

// Ops are the comparisons a template makes.
var Ops = []string{">", ">=", "<", "<=", "==", "!="}

// Template is a condition worth a notification, and what it says.
type Template struct {
	Name   string
	Desc   string
	Metric string
	Op     string
	Value  string // the threshold, in the metric's unit: 80, 500G, 24h, ONLINE
	// ErrorValue, if set, is a second threshold past which the alert is
	// an error, as a pool going from nearly full to full.
	ErrorValue string
	Severity   string // info, warning, error
	// Text is the line sent while the condition holds, ResolvedText the
	// line when it stops; both take placeholders (Placeholders).
	Text, ResolvedText string
	// Category groups the built-in templates in the Alerts tab; the user's
	// own have none.
	Category string
	// About says what the situation means for the pool and why it is
	// worth hearing of, in a sentence or two for the person deciding
	// whether to turn it on (as ZED's "impact:" line does).
	About string
}

// Categories order the built-in templates in the Alerts tab.
var Categories = []string{"Pool health", "Space", "Maintenance", "Performance", "System"}

// Placeholders are what a template's text can name.
var Placeholders = map[string]string{
	"{pool}":      "the pool, or \"the system\"",
	"{value}":     "the measured value, with its unit",
	"{threshold}": "the threshold it crossed",
	"{metric}":    "the measure's name",
	"{severity}":  "info, warning or error",
	"{detail}":    "what the backend adds, such as \"1 device OFFLINE\"",
}

// BuiltinTemplates are the templates fstui ships.
var BuiltinTemplates = []Template{
	{
		Name: "pool-unhealthy", About: "A pool that is DEGRADED has lost redundancy: one more failure can lose data. FAULTED or UNAVAIL means its data cannot be reached. Sent as soon as the state changes.", Category: "Pool health", Desc: "A pool is not ONLINE", Metric: "health", Op: "!=", Value: "ONLINE", Severity: "error",
		Text: "{pool} is {value}{detail}", ResolvedText: "{pool} is ONLINE again",
	},
	{
		Name: "device-down", About: "A disk has dropped out of its pool: unplugged, failed, or taken offline. A mirror or RAIDZ keeps working on what is left until the disk is back or replaced.", Category: "Pool health", Desc: "A device is offline, faulted, unavailable or removed", Metric: "devices_down", Op: ">", Value: "0", Severity: "error",
		Text: "{pool} has devices that are not online{detail}", ResolvedText: "{pool}: every device is online again",
	},
	{
		Name: "device-errors", About: "A disk returned read, write or checksum errors. One or two can be a loose cable or a bad sector; a count that keeps rising means the disk is failing.", Category: "Pool health", Desc: "Devices report read, write or checksum errors", Metric: "device_errors", Op: ">", Value: "0", Severity: "warning",
		Text: "{pool}: devices report {value} read, write or checksum errors", ResolvedText: "{pool}: device error counts are back to zero",
	},
	{
		Name: "data-errors", About: "ZFS found files it could not repair, because no good copy was left. zpool status -v lists them; restore them from a backup.", Category: "Pool health", Desc: "Files have permanent data errors", Metric: "data_errors", Op: ">", Value: "0", Severity: "error",
		Text: "{pool} has {value} data errors; zpool status -v {pool} lists the files", ResolvedText: "{pool} reports no data errors",
	},
	{
		Name: "capacity", About: "A pool slows down as it fills and stops taking writes when full. Hearing at 80% leaves time to free space or add disks.", Category: "Space", Desc: "A pool is filling up; an error when full", Metric: "capacity", Op: ">=", Value: "80", ErrorValue: "90", Severity: "warning",
		Text: "{pool} is {value} full (from {threshold})", ResolvedText: "{pool} is below {threshold} full again",
	},
	{
		Name: "low-free-space", About: "The same warning by the amount left instead of a percentage: better for a large pool, where 10% free is still a lot.", Category: "Space", Desc: "Free space drops below an amount", Metric: "free", Op: "<", Value: "100G", Severity: "warning",
		Text: "{pool} has {value} free (below {threshold})", ResolvedText: "{pool} has more than {threshold} free again",
	},
	{
		Name: "fragmentation", About: "When free space is split into small pieces, writes get slower. It eases as old data is deleted or rewritten.", Category: "Space", Desc: "Free space is fragmented, so writes may slow down", Metric: "fragmentation", Op: ">=", Value: "50", Severity: "warning",
		Text: "free space in {pool} is {value} fragmented", ResolvedText: "free space in {pool} is below {threshold} fragmented",
	},
	{
		Name: "scrub-overdue", About: "A scrub reads every block to find and repair damage while a good copy still exists. Monthly is usual; the Schedules tab can run them.", Category: "Maintenance", Desc: "A pool has not been scrubbed for a while", Metric: "scrub_age", Op: ">", Value: "35", Severity: "warning",
		Text: "{pool} was last scrubbed {value} ago (overdue after {threshold})", ResolvedText: "{pool} has been scrubbed",
	},
	{
		Name: "resilvering", About: "A resilver copies data onto a disk that was replaced or came back. Until it finishes the pool has less redundancy and is slower. Sent when it starts, and again when it is done.", Category: "Pool health", Desc: "A resilver runs", Metric: "resilvering", Op: ">", Value: "0", Severity: "info",
		Text: "{pool} is resilvering{detail}", ResolvedText: "{pool} finished resilvering",
	},
	{
		Name: "checkpoint-held", About: "While a checkpoint is held, no space is freed and disks cannot be removed or changed. Usually it was meant to be discarded after an upgrade.", Category: "Maintenance", Desc: "A checkpoint is held, blocking device changes", Metric: "checkpoint_age", Op: ">", Value: "24h", Severity: "warning",
		Text:         "{pool} has held a checkpoint for {value}; remove, attach, detach, split and reguid are blocked until it is discarded",
		ResolvedText: "{pool} holds no old checkpoint",
	},
	{
		Name: "slow-reads", About: "Reads are waiting longer than usual on the disks: often one disk failing slowly, or more work than the pool can take. Measured over one second at each check.", Category: "Performance", Desc: "Reads wait longer than usual", Metric: "read_wait", Op: ">", Value: "50", Severity: "warning",
		Text: "reads on {pool} wait {value} on average (over {threshold})", ResolvedText: "reads on {pool} wait less than {threshold} again",
	},
	{
		Name: "slow-writes", About: "Writes are waiting longer than usual on the disks: often one disk failing slowly, or more work than the pool can take. Measured over one second at each check.", Category: "Performance", Desc: "Writes wait longer than usual", Metric: "write_wait", Op: ">", Value: "50", Severity: "warning",
		Text: "writes on {pool} wait {value} on average (over {threshold})", ResolvedText: "writes on {pool} wait less than {threshold} again",
	},
	{
		Name: "version-mismatch", About: "The zfs commands and the running kernel module are different versions, usually after an update without a reboot. Some commands may fail until you reboot.", Category: "System", Desc: "ZFS tools and kernel module versions differ", Metric: "versions_differ", Op: ">", Value: "0", Severity: "warning",
		Text: "the ZFS userland and kernel module differ{detail}; reboot or reload the module", ResolvedText: "the userland and kernel module versions match",
	},
	{
		Name: "kernel-events", About: "ZFS reports events as they happen, such as a checksum error on a disk or an I/O error. This sends those of the chosen severity or worse.", Category: "System", Desc: "The kernel reports events, such as checksum errors", Metric: "events", Op: ">=", Value: "error", Severity: "warning",
		Text: "{detail}",
	},
}

// Alert turns a template on.
type Alert struct {
	Template string
	Pools    []string // empty: every pool
	Channels []string // empty: every channel
	// Value, ErrorValue, Severity, Text and ResolvedText override the
	// template's when set.
	Value, ErrorValue, Severity, Text, ResolvedText string
	Repeat                                          *time.Duration // nil: notifications.repeat
	Resolved                                        *bool          // nil: notifications.resolved
}

// TemplateNamed finds a template: the user's first, then the built-in
// ones, so the user can replace a built-in template by its name.
func (n Notifications) TemplateNamed(name string) (Template, bool) {
	for _, list := range [][]Template{n.Templates, BuiltinTemplates} {
		if i := slices.IndexFunc(list, func(t Template) bool { return t.Name == name }); i >= 0 {
			return list[i], true
		}
	}
	return Template{}, false
}

// Effective is a template with an alert's overrides.
func (a Alert) Effective(t Template) Template {
	for dst, src := range map[*string]string{&t.Value: a.Value, &t.ErrorValue: a.ErrorValue, &t.Severity: a.Severity, &t.Text: a.Text, &t.ResolvedText: a.ResolvedText} {
		if src != "" {
			*dst = src
		}
	}
	return t
}

// ActiveAlerts are the alerts in effect: notifications.alerts, or, when
// none are listed, what fstui sent before alerts existed, from the
// thresholds the Pools view warns at.
func (c Config) ActiveAlerts() []Alert {
	if len(c.Notifications.Alerts) > 0 {
		return c.Notifications.Alerts
	}
	th := c.Thresholds
	return []Alert{
		{Template: "pool-unhealthy"},
		{Template: "data-errors"},
		{Template: "capacity", Value: strconv.Itoa(th.CapacityWarn), ErrorValue: strconv.Itoa(th.CapacityError)},
		{Template: "fragmentation", Value: strconv.Itoa(th.FragmentationWarn)},
		{Template: "scrub-overdue", Value: strconv.Itoa(th.ScrubOverdueDays)},
		{Template: "checkpoint-held", Value: fmtDuration(th.CheckpointAge)},
		{Template: "version-mismatch"},
		{Template: "kernel-events", Value: c.Notifications.EventsMin, Severity: "error"},
	}
}

// Threshold reads a template's value in its metric's unit: a number for
// percent, count, days, ms and ratio; a size such as 500G for bytes; a
// duration such as 24h, in hours; a word for a state; a severity for
// events. A number comes back as number, a word as word.
func Threshold(metric, value string) (number float64, word string, err error) {
	m, ok := MetricNamed(metric)
	if !ok {
		return 0, "", fmt.Errorf("unknown measure %q", metric)
	}
	switch m.Kind {
	case "state":
		if !stateWord.MatchString(value) {
			return 0, "", fmt.Errorf("want a state such as ONLINE or DEGRADED")
		}
		return 0, strings.ToUpper(value), nil
	case "events":
		if !slices.Contains(SeverityNames, value) {
			return 0, "", fmt.Errorf("want one of %v", SeverityNames)
		}
		return 0, value, nil
	case "bytes":
		b, err := ParseBytes(value)
		return float64(b), "", err
	case "duration":
		d, err := time.ParseDuration(value)
		if err != nil || d < 0 {
			return 0, "", fmt.Errorf("want a duration such as 24h")
		}
		return d.Hours(), "", nil
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, "", fmt.Errorf("want a number of %s", m.Kind)
	}
	return f, "", nil
}

var stateWord = regexp.MustCompile(`^[A-Za-z]{2,16}$`)

// ParseBytes reads a size: a number of bytes, or one with K, M, G, T or
// P (powers of 1024), optionally followed by iB or B.
func ParseBytes(s string) (uint64, error) {
	t := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "B"), "i")
	mult := uint64(1)
	if t != "" {
		if i := strings.IndexByte("KMGTP", t[len(t)-1]); i >= 0 {
			mult = 1 << (10 * (i + 1))
			t = t[:len(t)-1]
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f < 0 || f*float64(mult) > float64(1<<62) {
		return 0, fmt.Errorf("want a size such as 500G")
	}
	return uint64(f * float64(mult)), nil
}

// Limits of the lists.
const (
	templatesMax = 64
	alertsMax    = 128
	textMax      = 300
)

var templateName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// setTemplates reads the user's templates; one with a problem is
// reported and left out. A file that lists templates replaces the list.
func setTemplates(a *applier, c *Config, n *yaml.Node) error {
	out, err := namedList(a, n, "notifications.templates", "template", templatesMax, templateOf, func(t Template) string { return t.Name })
	c.Notifications.Templates = out
	return err
}

func templateOf(n *yaml.Node) (Template, error) {
	t := Template{Op: ">", Severity: "warning"}
	err := fields(n, map[string]*string{
		"name": &t.Name, "description": &t.Desc, "metric": &t.Metric, "op": &t.Op, "value": &t.Value,
		"error_value": &t.ErrorValue, "severity": &t.Severity, "text": &t.Text, "resolved_text": &t.ResolvedText,
	}, nil)
	if err != nil {
		return t, err
	}
	return t, CheckNewTemplate(t)
}

// CheckNewTemplate checks a template the user wrote: its name and text,
// and what CheckTemplate checks.
func CheckNewTemplate(t Template) error {
	switch {
	case !templateName.MatchString(t.Name):
		return fmt.Errorf("name: want 1 to 40 lower-case letters, digits, - and _")
	case t.Text == "":
		return fmt.Errorf("text: say what the notification reports, e.g. \"{pool} is {value} full\"")
	}
	return CheckTemplate(t)
}

// CheckTemplate checks a template as an alert sets it: its measure,
// comparison, thresholds, severity and the length of its text.
func CheckTemplate(t Template) error {
	m, ok := MetricNamed(t.Metric)
	switch {
	case !ok:
		return fmt.Errorf("metric: want one of %s", strings.Join(metricNames(), ", "))
	case !slices.Contains(Ops, t.Op):
		return fmt.Errorf("op: want one of %s", strings.Join(Ops, " "))
	case m.Kind == "state" && t.Op != "==" && t.Op != "!=":
		return fmt.Errorf("op: a state is compared with == or !=")
	case m.Kind == "events" && t.Op != ">=":
		return fmt.Errorf("op: events are sent at or above a severity, with >=")
	case !slices.Contains(ProblemSeverities, t.Severity):
		return fmt.Errorf("severity: want one of %v", ProblemSeverities)
	case len(t.Text) > textMax || len(t.ResolvedText) > textMax:
		return fmt.Errorf("text: at most %d characters", textMax)
	}
	if _, _, err := Threshold(t.Metric, t.Value); err != nil {
		return fmt.Errorf("value: %w", err)
	}
	if t.ErrorValue != "" {
		if _, _, err := Threshold(t.Metric, t.ErrorValue); err != nil {
			return fmt.Errorf("error_value: %w", err)
		}
	}
	return nil
}

func metricNames() []string {
	out := make([]string, len(Metrics))
	for i, m := range Metrics {
		out[i] = m.Name
	}
	return out
}

// setAlerts reads the alerts. They are checked against the templates
// once every file is read (checkAlerts), since a template may come from a
// later file.
func setAlerts(a *applier, c *Config, n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("want a list of alerts, each naming a template")
	}
	if len(n.Content) > alertsMax {
		return fmt.Errorf("at most %d alerts", alertsMax)
	}
	var out []Alert
	for i, item := range n.Content {
		al, err := alertOf(item)
		if err != nil {
			a.report(item, "notifications.alerts[%d]: %v", i, err)
			continue
		}
		out = append(out, al)
	}
	c.Notifications.Alerts = out
	return nil
}

func alertOf(n *yaml.Node) (Alert, error) {
	var al Alert
	lists := map[string]*[]string{"pools": &al.Pools, "channels": &al.Channels}
	err := fields(n, map[string]*string{
		"template": &al.Template, "value": &al.Value, "error_value": &al.ErrorValue, "severity": &al.Severity,
		"text": &al.Text, "resolved_text": &al.ResolvedText,
	}, func(key string, v *yaml.Node) (bool, error) {
		switch key {
		case "pools", "channels":
			l, err := stringList(v)
			*lists[key] = l
			return true, err
		case "repeat":
			s, err := scalar(v, "!!str", "a duration such as 12h, or 0")
			if err != nil {
				return true, err
			}
			d, err := time.ParseDuration(s)
			if s == "0" {
				d, err = 0, nil
			}
			if err != nil || d < 0 || d > maxRepeat {
				return true, fmt.Errorf("want a duration from 0 to %s", fmtDuration(maxRepeat))
			}
			al.Repeat = &d
			return true, nil
		case "resolved":
			b, err := strconv.ParseBool(v.Value)
			if err != nil || v.Kind != yaml.ScalarNode {
				return true, fmt.Errorf("want true or false")
			}
			al.Resolved = &b
			return true, nil
		}
		return false, nil
	})
	if err == nil && al.Template == "" {
		err = fmt.Errorf("template: name the template it turns on")
	}
	return al, err
}

// checkAlerts reports alerts naming a template that does not exist, a
// channel that is not listed, or an override the template cannot take,
// and leaves them out.
func (c *Config) checkAlerts() []Problem {
	var out []Problem
	n := &c.Notifications
	kept := n.Alerts[:0]
	for i, al := range n.Alerts {
		err := c.alertError(al)
		if err != nil {
			out = append(out, Problem{File: "config", Msg: fmt.Sprintf("notifications.alerts[%d] (%s): %v", i, al.Template, err)})
			continue
		}
		kept = append(kept, al)
	}
	n.Alerts = kept
	return out
}

func (c *Config) alertError(al Alert) error {
	t, ok := c.Notifications.TemplateNamed(al.Template)
	if !ok {
		return fmt.Errorf("no template named %q", al.Template)
	}
	for _, ch := range al.Channels {
		if !slices.ContainsFunc(c.Notifications.Channels, func(x Channel) bool { return x.Name == ch }) {
			return fmt.Errorf("no channel named %q", ch)
		}
	}
	return CheckTemplate(al.Effective(t))
}

// fields reads a mapping of text fields; other handles the keys that are
// not text, and reports whether it knew the key.
func fields(n *yaml.Node, text map[string]*string, other func(string, *yaml.Node) (bool, error)) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("want a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if dst, ok := text[k.Value]; ok {
			if v.Kind != yaml.ScalarNode {
				return fmt.Errorf("%s: want text", k.Value)
			}
			*dst = v.Value
			continue
		}
		known := false
		if other != nil {
			var err error
			if known, err = other(k.Value, v); err != nil {
				return fmt.Errorf("%s: %w", k.Value, err)
			}
		}
		if !known {
			return fmt.Errorf("unknown key %q", k.Value)
		}
	}
	return nil
}

// stringList reads one word or a list of words.
func stringList(v *yaml.Node) ([]string, error) {
	switch v.Kind {
	case yaml.ScalarNode:
		return []string{v.Value}, nil
	case yaml.SequenceNode:
		out := make([]string, 0, len(v.Content))
		for _, it := range v.Content {
			if it.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("want names")
			}
			out = append(out, it.Value)
		}
		return out, nil
	case yaml.DocumentNode, yaml.MappingNode, yaml.AliasNode:
	}
	return nil, fmt.Errorf("want a name or a list of names")
}

// FormatDuration writes a duration as config.yaml takes it: 12h, 90m, 0.
func FormatDuration(d time.Duration) string { return fmtDuration(d) }

// opWords are the comparisons in a sentence.
var opWords = map[string]string{">": "above", ">=": "at or above", "<": "below", "<=": "at or below", "==": "is", "!=": "is not"}

// OpWords is a comparison in words: "above", "at or below".
func OpWords(op string) string { return opWords[op] }

// When is a template's threshold in a few words, for a list that already
// says what the template watches: "from 80%, error from 90%", "below 100
// GiB", "over 35 days". It is empty where the description says it all
// ("A device is offline").
func (t Template) When() string {
	m, _ := MetricNamed(t.Metric)
	short := map[string]string{">": "over", ">=": "from", "<": "below", "<=": "at most", "==": "when", "!=": "when not"}
	switch {
	case m.Kind == "events":
		return t.Value + " or worse"
	case m.Kind == "state" || m.Kind == "count" && t.Op == ">" && t.Value == "0":
		return ""
	}
	out := short[t.Op] + " " + FormatThreshold(t.Metric, t.Value)
	if t.ErrorValue != "" {
		out += ", error " + short[t.Op] + " " + FormatThreshold(t.Metric, t.ErrorValue)
	}
	return out
}

// Condition is a template's test in words, as "space used at or above
// 80%".
func (t Template) Condition() string {
	m, ok := MetricNamed(t.Metric)
	if !ok {
		return t.Metric + " " + t.Op + " " + t.Value
	}
	switch {
	case m.Kind == "events":
		return "kernel events of " + t.Value + " or worse"
	case m.Kind == "count" && t.Op == ">" && t.Value == "0":
		if m.Name == "resilvering" || m.Name == "versions_differ" {
			return m.Label
		}
		return "any " + m.Label
	}
	return m.Label + " " + opWords[t.Op] + " " + FormatThreshold(t.Metric, t.Value)
}

// FormatThreshold writes a threshold in its unit: 80%, 100 GiB, 24h.
func FormatThreshold(metric, value string) string {
	num, word, err := Threshold(metric, value)
	m, _ := MetricNamed(metric)
	switch {
	case err != nil || word != "" || m.Kind == "duration":
		return value // as typed: 24h reads better than 1d
	case m.Kind == "days" && num == 1:
		return "1 day"
	case m.Kind == "days":
		return strconv.FormatFloat(num, 'f', -1, 64) + " days"
	}
	return FormatValue(metric, num)
}

// FormatValue writes a measure in its unit, the way messages show it.
func FormatValue(metric string, v float64) string {
	m, _ := MetricNamed(metric)
	switch m.Kind {
	case "percent":
		return fmt.Sprintf("%.0f%%", v)
	case "bytes":
		return format.Size(uint64(math.Max(v, 0)), false)
	case "days":
		return format.Age(time.Duration(v * 24 * float64(time.Hour)))
	case "duration":
		return format.Age(time.Duration(v * float64(time.Hour)))
	case "ms":
		if v == math.Trunc(v) {
			return fmt.Sprintf("%.0f ms", v)
		}
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f", v)
}

// Fill puts a subject's values into a message's placeholders: the pool
// ("" is the system), the measured value as written, and the backend's
// detail.
func (t Template) Fill(text, subject, value, detail string) string {
	if subject == "" {
		subject = "the system"
	}
	return strings.NewReplacer(
		"{pool}", subject, "{value}", value, "{threshold}", FormatThreshold(t.Metric, t.Value),
		"{metric}", t.Metric, "{severity}", t.Severity, "{detail}", detail,
	).Replace(text)
}

// Example is a message filled in for a pool named tank with the metric's
// example value.
func (t Template) Example(text string) string {
	m, _ := MetricNamed(t.Metric)
	return t.Fill(text, "tank", FormatThreshold(t.Metric, m.Example), m.Detail)
}

// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/problem"
)

// routed is a problem an alert found, and who hears of it.
type routed struct {
	problem.Problem
	channels     []string       // nil: every channel
	repeat       *time.Duration // nil: notifications.repeat
	resolved     *bool          // nil: notifications.resolved
	resolvedText string
	event        bool // a kernel event: sent once, never resolved
}

// heardBy reports whether a channel is sent this problem.
func (p routed) heardBy(ch string) bool {
	return len(p.channels) == 0 || slices.Contains(p.channels, ch)
}

// needed are the metrics the active alerts compare, so the backend
// measures only those that cost something (I/O waits).
func needed(c config.Config) map[string]bool {
	out := map[string]bool{}
	for _, al := range c.ActiveAlerts() {
		if t, ok := c.Notifications.TemplateNamed(al.Template); ok {
			out[t.Metric] = true
		}
	}
	return out
}

// evaluate is what the active alerts find in the measures and the new
// kernel events.
func evaluate(c config.Config, ms []problem.Measure, evs []events.Event, boot string) []routed {
	var out []routed
	for _, al := range c.ActiveAlerts() {
		t, ok := c.Notifications.TemplateNamed(al.Template)
		if !ok {
			continue // reported when the configuration was read
		}
		t = al.Effective(t)
		base := routed{channels: al.Channels, repeat: al.Repeat, resolved: al.Resolved}
		if t.Metric == "events" {
			out = append(out, eventAlerts(t, al, base, evs, boot)...)
			continue
		}
		for _, m := range ms {
			if p, ok := fires(t, al, m); ok {
				r := base
				r.Problem, r.resolvedText = p, render(t.ResolvedText, t, m, "")
				out = append(out, r)
			}
		}
	}
	return dedupe(out)
}

// fires reports whether template t, as alert al sets it, holds for m.
func fires(t config.Template, al config.Alert, m problem.Measure) (problem.Problem, bool) {
	if len(al.Pools) > 0 && !slices.Contains(al.Pools, m.Subject) {
		return problem.Problem{}, false
	}
	num, word, _ := config.Threshold(t.Metric, t.Value) // checked when read
	sev := severityOf(t.Severity)
	state := t.Severity
	if word != "" {
		v, ok := m.States[t.Metric]
		if !ok || !compareWords(v, t.Op, word) {
			return problem.Problem{}, false
		}
		state = v // a pool going from DEGRADED to FAULTED is news
	} else {
		v, ok := m.Numbers[t.Metric]
		if !ok || !compare(v, t.Op, num) {
			return problem.Problem{}, false
		}
		if t.ErrorValue != "" {
			if e, _, _ := config.Threshold(t.Metric, t.ErrorValue); compare(v, t.Op, e) {
				sev, state = problem.Error, "error"
			}
		}
	}
	subject := m.Subject
	if subject == "" {
		subject = "system"
	}
	return problem.Problem{
		Key: "alert:" + t.Name + "@" + t.Value + ":" + subject, State: state, Severity: sev,
		Subject: m.Subject, Kind: t.Name, Text: render(t.Text, t, m, ""),
	}, true
}

// eventAlerts are the new kernel events at or above the alert's
// severity, in its pools.
func eventAlerts(t config.Template, al config.Alert, base routed, evs []events.Event, boot string) []routed {
	least := events.Severity(slices.Index(config.SeverityNames, t.Value) + 1) // events.Info is 1
	var out []routed
	for _, e := range evs {
		pool := e.Source.Raw()
		if e.Severity < least || len(al.Pools) > 0 && !slices.Contains(al.Pools, pool) {
			continue
		}
		sev := map[events.Severity]problem.Severity{events.Error: problem.Error, events.Warn: problem.Warning}[e.Severity]
		if al.Severity != "" {
			sev = severityOf(al.Severity)
		}
		text := e.Source.Display() + ": " + e.Summary.Display()
		if d := e.Device.Display(); d != "" {
			text += " (" + d + ")"
		}
		text += "; " + e.Class.Display()
		m := problem.Measure{Subject: pool}
		r := base
		r.event = true
		r.Problem = problem.Problem{
			Key: fmt.Sprintf("event:%s:%d", boot, e.ID), Severity: sev, Subject: pool, Kind: t.Name,
			Text: render(t.Text, t, m, text),
		}
		out = append(out, r)
	}
	return out
}

// dedupe keeps one problem per key: two alerts of the same template and
// threshold on the same pool are one problem.
func dedupe(ps []routed) []routed {
	seen := map[string]bool{}
	return slices.DeleteFunc(ps, func(p routed) bool {
		if seen[p.Key] {
			return true
		}
		seen[p.Key] = true
		return false
	})
}

func compare(v float64, op string, t float64) bool {
	switch op {
	case ">":
		return v > t
	case ">=":
		return v >= t
	case "<":
		return v < t
	case "<=":
		return v <= t
	case "==":
		return v == t
	case "!=":
		return v != t
	}
	return false
}

func compareWords(v, op, t string) bool {
	eq := strings.EqualFold(v, t)
	return op == "==" && eq || op == "!=" && !eq
}

// render fills a template's placeholders for subject m; detail replaces
// the backend's detail (a kernel event's text).
func render(text string, t config.Template, m problem.Measure, detail string) string {
	if detail == "" {
		detail = m.Details[t.Metric]
	}
	value := m.States[t.Metric]
	if v, ok := m.Numbers[t.Metric]; ok {
		value = config.FormatValue(t.Metric, v)
	}
	return t.Fill(text, m.Subject, value, detail)
}

// eventsLeast is the least severe kernel event any active alert sends;
// ok is false when no alert sends kernel events.
func eventsLeast(c config.Config) (events.Severity, bool) {
	least, ok := events.Severity(math.MaxInt8), false
	for _, al := range c.ActiveAlerts() {
		t, found := c.Notifications.TemplateNamed(al.Template)
		if !found || t.Metric != "events" {
			continue
		}
		t = al.Effective(t)
		least, ok = min(least, events.Severity(slices.Index(config.SeverityNames, t.Value)+1)), true
	}
	return least, ok
}

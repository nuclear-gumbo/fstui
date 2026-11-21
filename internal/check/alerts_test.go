// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/problem"
)

func parsed(t *testing.T, yaml string) config.Config {
	t.Helper()
	c, problems := config.Parse("config.yaml", []byte(yaml))
	if len(problems) > 0 {
		t.Fatalf("config: %v", problems)
	}
	return c
}

func pool(name string, nums map[string]float64, states map[string]string) problem.Measure {
	return problem.Measure{Subject: name, Numbers: nums, States: states}
}

// A custom template on chosen pools reaches only the channels its alert
// names, with its own text, and turns into an error past error_value.
func TestCustomAlert(t *testing.T) {
	c := parsed(t, `
notifications:
  channels:
    - {name: phone, type: ntfy, url: "https://n", topic: z, min_severity: info}
    - {name: mail, type: webhook, url: "https://w", min_severity: info}
  templates:
    - name: tight
      metric: free
      op: "<"
      value: 500G
      error_value: 100G
      severity: warning
      text: "{pool} has only {value} left (alarm below {threshold})"
      resolved_text: "{pool} has room again"
  alerts:
    - {template: tight, pools: [vault], channels: [mail]}
    - {template: slow-reads, value: "20"}
`)
	ms := []problem.Measure{
		pool("vault", map[string]float64{"free": 300 << 30, "read_wait": 25}, nil),
		pool("tank", map[string]float64{"free": 50 << 30, "read_wait": 5}, nil),
	}
	found := evaluate(c, ms, nil, "b")
	if len(found) != 2 {
		t.Fatalf("found %+v", found)
	}
	free, slow := found[0], found[1]
	if free.Subject != "vault" || free.Severity != problem.Warning || free.Text != "vault has only 300 GiB left (alarm below 500 GiB)" || !free.heardBy("mail") || free.heardBy("phone") {
		t.Errorf("free: %+v", free)
	}
	if slow.Subject != "vault" || !strings.Contains(slow.Text, "wait 25 ms on average (over 20 ms)") || !slow.heardBy("phone") {
		t.Errorf("slow reads: %+v", slow)
	}
	items := decide(Rules{Repeat: time.Hour, Resolved: true}, c.Notifications.Channels, State{Problems: map[string]*Entry{}}, found, time.Now())
	if len(items["mail"]) != 2 || len(items["phone"]) != 1 {
		t.Errorf("routing: %+v", items)
	}

	ms[0].Numbers["free"] = 80 << 30
	if f := evaluate(c, ms, nil, "b")[0]; f.Severity != problem.Error || f.State != "error" {
		t.Errorf("past error_value: %+v", f)
	}

	// Cleared: the resolved line is the template's, kept in the state.
	now := time.Now()
	prev := nextState(State{Problems: map[string]*Entry{}}, found, map[string]bool{"mail": true}, items, now)
	items = decide(Rules{Repeat: time.Hour, Resolved: true}, c.Notifications.Channels, prev, nil, now)
	if got := items["mail"]; len(got) != 2 || got[1].Text != "vault has room again" && got[0].Text != "vault has room again" {
		t.Errorf("resolved: %+v", got)
	}
}

// Without alerts, the defaults are 0.8.0's: health, data errors,
// capacity from the thresholds, and so on.
func TestDefaultAlertsFollowThresholds(t *testing.T) {
	c := parsed(t, "thresholds: {capacity_warn: 60, capacity_error: 70}\n")
	ms := []problem.Measure{pool("tank", map[string]float64{"capacity": 65}, map[string]string{"health": "DEGRADED"})}
	found := evaluate(c, ms, nil, "b")
	var kinds []string
	for _, f := range found {
		kinds = append(kinds, f.Kind+"/"+f.Severity.String())
	}
	if got := strings.Join(kinds, " "); got != "pool-unhealthy/error capacity/warning" {
		t.Errorf("found %s", got)
	}
}

// A per-alert repeat and resolved override the notifications settings.
func TestAlertRepeatAndResolved(t *testing.T) {
	c := parsed(t, `
notifications:
  channels: [{name: phone, type: ntfy, url: "https://n", topic: z}]
  alerts: [{template: capacity, value: "50", repeat: 1h, resolved: false}]
`)
	ms := []problem.Measure{pool("tank", map[string]float64{"capacity": 55}, nil)}
	found := evaluate(c, ms, nil, "b")
	now := time.Now()
	items := decide(Rules{Repeat: 24 * time.Hour, Resolved: true}, c.Notifications.Channels, State{Problems: map[string]*Entry{}}, found, now)
	prev := nextState(State{Problems: map[string]*Entry{}}, found, map[string]bool{"phone": true}, items, now)
	if again := decide(Rules{Repeat: 24 * time.Hour, Resolved: true}, c.Notifications.Channels, prev, found, now.Add(2*time.Hour)); len(again["phone"]) != 1 || again["phone"][0].Kind != Again {
		t.Errorf("repeat 1h: %+v", again)
	}
	if gone := decide(Rules{Repeat: 24 * time.Hour, Resolved: true}, c.Notifications.Channels, prev, nil, now); len(gone["phone"]) != 0 {
		t.Errorf("resolved: false still sent %+v", gone)
	}
}

// Mistakes in templates and alerts are reported with what is wrong, and
// the rest of the file applies.
func TestAlertConfigProblems(t *testing.T) {
	for yaml, want := range map[string]string{
		"notifications: {alerts: [{template: nope}]}\n":                                                `no template named "nope"`,
		"notifications: {alerts: [{template: capacity, channels: [ghost]}]}\n":                         `no channel named "ghost"`,
		"notifications: {alerts: [{template: capacity, value: lots}]}\n":                               "value: want a number",
		"notifications: {templates: [{name: x, metric: health, op: \">\", value: ONLINE, text: t}]}\n": "a state is compared with == or !=",
		"notifications: {templates: [{name: x, metric: free, op: \"<\", value: 5Q, text: t}]}\n":       "want a size",
		"notifications: {templates: [{name: x, metric: bogus, op: \">\", value: \"1\", text: t}]}\n":   "metric: want one of",
		"notifications: {templates: [{name: x, metric: capacity, op: \">\", value: \"1\"}]}\n":         "text: say what",
	} {
		_, problems := config.Parse("config.yaml", []byte(yaml))
		if len(problems) == 0 || !strings.Contains(problems[0].String(), want) {
			t.Errorf("%q: %v, want %q", yaml, problems, want)
		}
	}
}

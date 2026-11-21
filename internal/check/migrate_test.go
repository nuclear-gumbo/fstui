// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/problem"
)

// After an upgrade from 0.8.0, problems still open are not sent again
// and none is said to have cleared.
func TestMigrateFrom080(t *testing.T) {
	c := config.Default()
	c.Notifications.Channels = []config.Channel{{Name: "phone", Type: "ntfy", URL: "https://n", Topic: "z", MinSeverity: "info"}}
	sent := map[string]time.Time{"phone": time.Now().Add(-time.Hour)}
	old := State{Version: stateVersion, Problems: map[string]*Entry{
		"health:backup": {Severity: "error", State: "DEGRADED", Sent: sent},
		"capacity:tank": {Severity: "error", State: "FULL", Sent: sent},
		"scrub:scratch": {Severity: "warning", State: "2026-11-01 00:00:00 +0000 UTC", Sent: sent},
		"version":       {Severity: "warning", State: "2.3.9 2.3.8", Sent: sent},
		"check:read":    {Severity: "error", State: "failed", Sent: sent},
	}}
	ms := []problem.Measure{
		{Numbers: map[string]float64{"versions_differ": 1}},
		{Subject: "backup", Numbers: map[string]float64{"capacity": 10}, States: map[string]string{"health": "DEGRADED"}},
		{Subject: "tank", Numbers: map[string]float64{"capacity": 95}, States: map[string]string{"health": "ONLINE"}},
		{Subject: "scratch", Numbers: map[string]float64{"scrub_age": 40}, States: map[string]string{"health": "ONLINE"}},
	}
	found := append(evaluate(c, ms, nil, "b"), routed{Problem: problem.Problem{Key: readProblem, State: "failed", Severity: problem.Error}})
	items := decide(Rules{Repeat: 24 * time.Hour, Resolved: true}, c.Notifications.Channels, migrate(old, c), found, time.Now())
	if len(items["phone"]) != 0 {
		t.Errorf("sent after the upgrade: %+v", items["phone"])
	}
}

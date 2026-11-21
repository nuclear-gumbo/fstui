// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"reflect"
	"testing"
	"time"
)

// Alerts and templates written by the Config view read back the same,
// and the rest of the file stays.
func TestEditAlertsRoundTrip(t *testing.T) {
	h := 2 * time.Hour
	no := false
	alerts := []Alert{
		{Template: "capacity", Pools: []string{"tank", "vault"}, Channels: []string{"phone"}, Value: "70", ErrorValue: "85", Repeat: &h, Resolved: &no},
		{Template: "tight"},
	}
	templates := []Template{{Name: "tight", Metric: "free", Op: "<", Value: "500G", Severity: "error", Text: "{pool} has {value} left"}}
	in := []byte("# mine\ntheme: nord\nnotifications:\n  channels: [{name: phone, type: ntfy, url: \"https://n\", topic: z}]\n")
	out, err := EditAlerts(in, alerts, templates)
	if err != nil {
		t.Fatal(err)
	}
	c, problems := Parse("config.yaml", out)
	if len(problems) > 0 {
		t.Fatalf("%v\n%s", problems, out)
	}
	if c.Theme != "nord" || !reflect.DeepEqual(c.Notifications.Alerts, alerts) || !reflect.DeepEqual(c.Notifications.Templates, templates) {
		t.Errorf("read back:\n%s\n%+v\n%+v", out, c.Notifications.Alerts, c.Notifications.Templates)
	}
	out, _ = EditAlerts(out, nil, nil)
	if c, _ := Parse("config.yaml", out); len(c.Notifications.Alerts) != 0 || len(c.Notifications.Templates) != 0 || len(c.Notifications.Channels) != 1 {
		t.Errorf("emptied:\n%s", out)
	}
}

// Thresholds read as a person writes them.
func TestFormatThreshold(t *testing.T) {
	for _, c := range [][3]string{
		{"capacity", "80", "80%"},
		{"free", "500G", "500 GiB"},
		{"scrub_age", "1", "1 day"},
		{"scrub_age", "35", "35 days"},
		{"checkpoint_age", "24h", "24h"},
		{"read_wait", "50", "50 ms"},
		{"health", "ONLINE", "ONLINE"},
	} {
		if got := FormatThreshold(c[0], c[1]); got != c[2] {
			t.Errorf("%s %s: %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestEventsSection(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", `events:
  retention: 2000
  filters:
    - name: errors only
      severity: [error, warn]
    - name: tank
      pool: tank
      class: checksum
    - name: bad severity
      severity: urgent
    - pool: no name
    - name: tank
      text: duplicate
    - name: typo
      pol: tank
`)
	c, ps := testTrust(dir).load([]string{f})
	if c.Events.Retention != 2000 {
		t.Errorf("retention %d", c.Events.Retention)
	}
	var names []string
	for _, f := range c.Events.Filters {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "errors only,tank" {
		t.Errorf("filters %v", names)
	}
	if got := c.Events.Filters[0].Severities; strings.Join(got, ",") != "error,warn" {
		t.Errorf("severities %v", got)
	}
	if got := c.Events.Filters[1]; got.Pool != "tank" || got.Class != "checksum" {
		t.Errorf("tank filter %+v", got)
	}
	// One problem each for the bad severity, the missing name, the
	// duplicate and the typo, with their lines.
	var lines []int
	for _, p := range ps {
		lines = append(lines, p.Line)
	}
	if len(ps) != 4 || lines[0] != 9 || lines[1] != 11 || lines[2] != 12 || lines[3] != 14 {
		t.Errorf("problems %v", ps)
	}
}

func TestEventsDefaultsAndRange(t *testing.T) {
	if Default().Events.Retention != 5000 {
		t.Error("default retention is not 5000")
	}
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", "events:\n  retention: 10\n")
	c, ps := testTrust(dir).load([]string{f})
	if len(ps) != 1 || c.Events.Retention != 5000 {
		t.Errorf("retention 10: %v, kept %d", ps, c.Events.Retention)
	}
}

func TestDashboardSection(t *testing.T) {
	if got := Default().Dashboard; strings.Join(got.Panels, ",") != "health,events,io,arc" || got.Columns != 0 {
		t.Errorf("defaults %+v", got)
	}
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", "dashboard:\n  panels: [scans, health]\n  columns: 2\n")
	c, ps := testTrust(dir).load([]string{f})
	if len(ps) != 0 || strings.Join(c.Dashboard.Panels, ",") != "scans,health" || c.Dashboard.Columns != 2 {
		t.Errorf("%+v, %v", c.Dashboard, ps)
	}
	f = write(t, dir, "config.yaml", "dashboard:\n  panels: [health, helth]\n  columns: 4\n")
	c, ps = testTrust(dir).load([]string{f})
	if len(ps) != 2 || strings.Join(c.Dashboard.Panels, ",") != "health,events,io,arc" || c.Dashboard.Columns != 0 {
		t.Errorf("typo and range: %+v, %v", c.Dashboard, ps)
	}
}

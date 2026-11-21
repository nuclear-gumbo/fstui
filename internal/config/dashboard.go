// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Dashboard is the dashboard: section.
type Dashboard struct {
	Panels  []string // in order; names from DashboardPanels
	Columns int      // 1 to 3, or 0 to choose by terminal width
	// With columns 0: two columns from this terminal width, three from
	// ThreeColumnsFrom.
	TwoColumnsFrom, ThreeColumnsFrom int
}

// DashboardPanels are the panels the dashboard can show.
var DashboardPanels = []string{"health", "events", "io", "arc", "scans"}

const dashboardColumnsMax = 3

func defaultDashboard() Dashboard {
	return Dashboard{Panels: []string{"health", "events", "io", "arc"}, TwoColumnsFrom: 120, ThreeColumnsFrom: 200}
}

// setPanels reads the list of panels; an unknown or repeated name is an
// error for the whole list, so a typo cannot silently hide a panel.
func setPanels(_ *applier, c *Config, n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
		return fmt.Errorf("want a list of panels from %q", DashboardPanels)
	}
	var out []string
	for _, item := range n.Content {
		name, err := oneOf(item, DashboardPanels...)
		if err != nil {
			return err
		}
		if slices.Contains(out, name) {
			return fmt.Errorf("%q is listed twice", name)
		}
		out = append(out, name)
	}
	c.Dashboard.Panels = out
	return nil
}

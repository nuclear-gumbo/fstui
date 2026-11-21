// SPDX-License-Identifier: GPL-3.0-or-later

package problem

// Measure is what a backend measured of one subject: a pool, or the
// system when Subject is empty. Alerts compare these (config.Metrics
// names them); a backend reports what it can measure.
type Measure struct {
	Subject string
	Numbers map[string]float64 // by metric name, in the metric's unit
	States  map[string]string  // by metric name, for state metrics
	// Details complete a template's {detail}, by metric name, such as
	// ": 1 device OFFLINE" for devices_down.
	Details map[string]string
}

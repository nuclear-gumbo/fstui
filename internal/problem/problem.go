// SPDX-License-Identifier: GPL-3.0-or-later

// Package problem is what any storage backend reports as wrong: a pool
// that is degraded, a filesystem nearly full, a scrub overdue. The views
// list problems, fstui check sends them; neither needs to know which
// backend found them.
package problem

// Severity orders problems; a notification channel takes those at or
// above its minimum.
type Severity int

// Severities.
const (
	Info Severity = iota
	Warning
	Error
)

// String is the severity as configuration and messages write it.
func (s Severity) String() string {
	return [...]string{"info", "warning", "error"}[s]
}

// Problem is one thing wrong.
type Problem struct {
	// Key is stable across reads, e.g. "capacity:tank".
	Key string
	// State is what must change for the problem to count as new again:
	// the threshold crossed, not the exact figure, so a pool going from
	// 93% to 94% full is the same problem, and one becoming FULL is not.
	State    string
	Severity Severity
	Text     string
	// Advisory problems can be hidden in a view and are not shown in
	// superuser mode. Health and data errors are state, not advice.
	Advisory bool
	// Subject is the pool, or "" for the system; Kind is what found it
	// (an alert template's name), for filtering and messages.
	Subject, Kind string
}

// SPDX-License-Identifier: GPL-3.0-or-later

// Package events is the backend-neutral event stream: what the kernel reported
// about pools and devices, in one shape for every backend, so the Events view
// and the dashboard need not know where an event came from.
package events

import (
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Severity orders events by how much attention they need.
type Severity int

// Severities, least to most urgent.
const (
	Info Severity = iota + 1
	Notice
	Warn
	Error
)

// String is the word the UI shows, always next to any colour.
func (s Severity) String() string {
	switch s {
	case Info:
		return "INFO"
	case Notice:
		return "NOTICE"
	case Warn:
		return "WARN"
	case Error:
		return "ERROR"
	}
	return "?"
}

// Field is one name and value from an event's payload.
type Field struct {
	Name  string
	Value termtext.Text
}

// Event is one report from a backend.
type Event struct {
	// ID orders a backend's events and tells them apart; ZFS's eid.
	ID       uint64
	Time     time.Time
	Backend  string        // "zfs"
	Source   termtext.Text // pool or filesystem name
	Device   termtext.Text // device path, may be empty
	Class    termtext.Text // the backend's own class, e.g. ereport.fs.zfs.checksum
	Severity Severity
	Summary  termtext.Text // one line, plain words
	Detail   []Field       // the whole payload, in the backend's order
}

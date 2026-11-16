// SPDX-License-Identifier: GPL-3.0-or-later

package events

import "slices"

// Log keeps the events a backend has reported, oldest first, bounded by a
// retention count. Backends are read by polling, so each read repeats events
// already kept; Log keeps only those newer than the last one seen, by ID.
type Log struct {
	max     int
	events  []Event
	last    map[string]uint64 // newest ID kept, per backend
	dropped int
}

// NewLog returns a log keeping at most limit events.
func NewLog(limit int) *Log { return &Log{max: limit, last: map[string]uint64{}} }

// Add takes one read of a backend's events, oldest first, and keeps the
// ones not seen before; dropped is how many the backend said it lost. It
// returns the new events.
//
// IDs only grow while the kernel module stays loaded. A read whose newest
// ID is below the last one kept means the module was reloaded and started
// counting again, so the whole read is new.
func (l *Log) Add(backend string, read []Event, dropped int) []Event {
	l.dropped += dropped
	if len(read) == 0 {
		return nil
	}
	last, seen := l.last[backend]
	if seen && read[len(read)-1].ID < last {
		last = 0
	}
	var fresh []Event
	for _, e := range read {
		if !seen || e.ID > last {
			fresh = append(fresh, e)
		}
	}
	l.last[backend] = read[len(read)-1].ID
	l.events = append(l.events, fresh...)
	l.trim()
	return fresh
}

// Events returns the kept events, oldest first. The slice is the log's
// own: valid until the next Add or SetRetention, so callers read it at
// once and keep copies, never the slice.
func (l *Log) Events() []Event { return l.events }

// Dropped is how many events the backends reported losing.
func (l *Log) Dropped() int { return l.dropped }

// SetRetention changes the retention count, dropping the oldest events
// if there are now too many.
func (l *Log) SetRetention(limit int) {
	l.max = limit
	l.trim()
}

// trim drops the oldest events beyond the retention. The kept ones move
// to the front, so the dropped ones (and their strings) can be freed at
// once instead of staying behind in the slice's array.
func (l *Log) trim() {
	if over := len(l.events) - l.max; over > 0 {
		l.events = slices.Delete(l.events, 0, over)
	}
}

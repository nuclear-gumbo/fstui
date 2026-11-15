// SPDX-License-Identifier: GPL-3.0-or-later

package events

import "testing"

func ids(evs []Event) []uint64 {
	out := make([]uint64, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}

func batch(from, to uint64) []Event {
	var out []Event
	for id := from; id <= to; id++ {
		out = append(out, Event{ID: id, Backend: "zfs"})
	}
	return out
}

func TestLogKeepsOnlyNewEvents(t *testing.T) {
	l := NewLog(100)
	if got := l.Add("zfs", batch(1, 3), 0); len(got) != 3 {
		t.Fatalf("first read: %v", ids(got))
	}
	// The next poll repeats 1-3 and adds 4-5.
	if got := l.Add("zfs", batch(1, 5), 2); len(got) != 2 || got[0].ID != 4 {
		t.Fatalf("second read: %v", ids(got))
	}
	if len(l.Events()) != 5 || l.Dropped() != 2 {
		t.Errorf("kept %v, dropped %d", ids(l.Events()), l.Dropped())
	}
	if got := l.Add("zfs", batch(1, 5), 0); got != nil {
		t.Errorf("a repeated read added %v", ids(got))
	}
}

// After a module reload the kernel counts from the start again.
func TestLogAfterModuleReload(t *testing.T) {
	l := NewLog(100)
	l.Add("zfs", batch(100, 110), 0)
	if got := l.Add("zfs", batch(1, 2), 0); len(got) != 2 {
		t.Fatalf("after reload: %v", ids(got))
	}
	if got := l.Add("zfs", batch(1, 3), 0); len(got) != 1 || got[0].ID != 3 {
		t.Errorf("next read: %v", ids(got))
	}
}

func TestLogRetention(t *testing.T) {
	l := NewLog(3)
	l.Add("zfs", batch(1, 5), 0)
	if got := ids(l.Events()); len(got) != 3 || got[0] != 3 {
		t.Errorf("kept %v", got)
	}
	l.SetRetention(1)
	if got := ids(l.Events()); len(got) != 1 || got[0] != 5 {
		t.Errorf("after shrinking: %v", got)
	}
}

func TestSeverityWords(t *testing.T) {
	for s, want := range map[Severity]string{Info: "INFO", Notice: "NOTICE", Warn: "WARN", Error: "ERROR"} {
		if s.String() != want {
			t.Errorf("%d = %q", s, s.String())
		}
	}
}

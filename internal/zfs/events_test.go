// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

func TestParseEvents(t *testing.T) {
	evs, dropped, err := ParseEvents(golden(t, "events", "induced-v.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 93 || dropped != 0 {
		t.Fatalf("%d events, %d dropped", len(evs), dropped)
	}
	counts := map[string]int{}
	for i, e := range evs {
		counts[e.Class.Raw()]++
		if i > 0 && e.ID <= evs[i-1].ID {
			t.Errorf("event %d: eid %d after %d", i, e.ID, evs[i-1].ID)
		}
		if e.Source.Raw() != "fstuitest_ev" || e.Backend != "zfs" || e.Summary.IsEmpty() {
			t.Errorf("event %d: %+v", i, e)
		}
	}
	// As captured: 20 checksum errors from the overwritten side, the
	// OFFLINE and back ONLINE state changes, two scrubs, one resilver.
	if counts["ereport.fs.zfs.checksum"] != 20 || counts["resource.fs.zfs.statechange"] != 2 ||
		counts["sysevent.fs.zfs.scrub_finish"] != 2 || counts["sysevent.fs.zfs.resilver_finish"] != 1 {
		t.Errorf("classes %v", counts)
	}

	// The first event as captured: the pool's creation at
	// 10:32:58.677022557 local time, eid 0x1532.
	first := evs[0]
	if first.ID != 0x1532 || !first.Time.Equal(time.Unix(0x6af88d3a, 0x285a8b5d)) || first.Severity != events.Info ||
		first.Summary.Raw() != "Pool created" {
		t.Errorf("first event: id %#x time %v %s %q", first.ID, first.Time, first.Severity, first.Summary.Raw())
	}
	for _, e := range evs {
		checkInducedEvent(t, e)
	}
}

// checkInducedEvent checks one event from the induced-faults capture
// against what was done to the pool.
func checkInducedEvent(t *testing.T, e events.Event) {
	t.Helper()
	switch e.Class.Raw() {
	case "ereport.fs.zfs.checksum":
		if e.Severity != events.Error || e.Summary.Raw() != "Checksum error" || e.Device.Raw() != "/var/tmp/fstui-test/fstuitest_ev-b.img" {
			t.Errorf("checksum: %s %q on %q", e.Severity, e.Summary.Raw(), e.Device.Raw())
		}
	case "resource.fs.zfs.statechange":
		switch e.Summary.Raw() {
		case "Device OFFLINE (was ONLINE)":
			if e.Severity != events.Warn {
				t.Errorf("offline is %s", e.Severity)
			}
		case "Device ONLINE (was OFFLINE)":
		default:
			t.Errorf("statechange %q", e.Summary.Raw())
		}
	case "sysevent.fs.zfs.history_event":
		if e.Summary.Raw() == "" || strings.Contains(e.Summary.Raw(), `"`) {
			t.Errorf("history summary %q", e.Summary.Raw())
		}
	}
}

func TestParseEventsEdges(t *testing.T) {
	in := "dropped 221 events\n" +
		"Nov 14 2026 10:00:00.000000000\tsysevent.fs.zfs.made_up\n" +
		"        class = \"sysevent.fs.zfs.made_up\"\n" +
		"        detector = (embedded nvlist)\n" +
		"                pool = 0x1\n" +
		"        (end detector)\n" +
		"        pool = \"tank\"\n" +
		"        time = 0x10 0x0 \n" +
		"        eid = 0x7\n\n"
	evs, dropped, err := ParseEvents([]byte(in))
	if err != nil || dropped != 221 || len(evs) != 1 {
		t.Fatalf("%v, %d dropped, %d events", err, dropped, len(evs))
	}
	e := evs[0]
	// The nested detector's pool (a GUID) must not replace the pool name;
	// an unknown class is a notice named after itself.
	if e.Source.Raw() != "tank" || e.Severity != events.Notice || e.Summary.Raw() != "made_up" || e.ID != 7 {
		t.Errorf("%+v", e)
	}
	if _, _, err := ParseEvents([]byte("Nov 14 2026 10:00:00.0\tsysevent.fs.zfs.x\n        pool = \"t\"\n\n")); err == nil {
		t.Error("an event without eid and time was accepted")
	}
}

// refusingCommands answers like zpool events does for a user who is not
// root: exit status 255 and this message.
type refusingCommands struct{}

func (refusingCommands) Read(_ context.Context, a execute.Argv) (execute.Result, error) {
	msg := termtext.New("cannot get event: permission denied\n")
	return execute.Result{ExitCode: 255, Stderr: msg}, &execute.CommandError{Argv: a, ExitCode: 255, Stderr: msg}
}

func (refusingCommands) Stream(context.Context, execute.Argv, func([]byte)) error { return nil }

func TestEventsNeedRoot(t *testing.T) {
	_, _, err := NewReader(refusingCommands{}).Events(context.Background())
	if !errors.Is(err, ErrEventsNeedRoot) {
		t.Errorf("err = %v, want ErrEventsNeedRoot", err)
	}
}

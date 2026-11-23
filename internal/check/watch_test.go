// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// watchLoop runs Watch.Loop with load; each pass calls then with
// its number, from the loop's goroutine, so then may change the source
// without racing the loop.
func watchLoop(t *testing.T, load func() ([]Run, []string, error), wake chan struct{}, then func(pass int, cancel func())) (passes int, failed []error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	w := Watch{
		Load: load,
		Report: func(Result, []string) {
			passes++
			then(passes, cancel)
		},
		Fail: func(err error) { failed = append(failed, err) },
	}
	w.Loop(ctx, wake)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("the loop did not finish; %d passes", passes)
	}
	return passes, failed
}

// A new kernel event runs a pass long before the interval, and is sent
// once.
func TestWatchRunsOnNewEvent(t *testing.T) {
	h := newHarness(t)
	h.run.Config.Notifications.WatchInterval = time.Hour
	h.run.Config.Notifications.WatchEvents = 5 * time.Millisecond
	h.src.evs = []events.Event{event(1, events.Error, "old")}
	load := func() ([]Run, []string, error) { return []Run{h.run}, nil, nil }
	passes, _ := watchLoop(t, load, nil, func(pass int, cancel func()) {
		switch pass {
		case 1: // the first run only notes where the events stand
			h.src.evs = append(h.src.evs, event(2, events.Error, "Checksum error"))
		case 2:
			cancel()
		}
	})
	if got := titles(h.out.take("phone")); passes != 2 || got != "nas: tank: Checksum error; ereport.fs.zfs.checksum" {
		t.Errorf("%d passes, sent %q", passes, got)
	}
}

// A failed load is reported and retried; wake (SIGHUP) runs a pass at
// once instead of waiting for the interval.
func TestWatchWakeAndFailedLoad(t *testing.T) {
	h := newHarness(t)
	h.run.Config.Notifications.WatchInterval = time.Hour
	wake := make(chan struct{}, 1)
	loads := 0
	load := func() ([]Run, []string, error) {
		loads++
		if loads == 1 {
			wake <- struct{}{}
			return nil, nil, errors.New("config unreadable")
		}
		return []Run{h.run}, nil, nil
	}
	passes, failed := watchLoop(t, load, wake, func(pass int, cancel func()) {
		if pass == 1 {
			wake <- struct{}{}
			return
		}
		cancel()
	})
	if passes != 2 || len(failed) != 1 {
		t.Errorf("%d passes, failures %v", passes, failed)
	}
}

// Nothing is pending while events are off or left to ZED, before the
// first pass, or when every new event is below events_min.
func TestEventsPending(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	h.src.evs = []events.Event{event(1, events.Error, "old")}
	if h.run.EventsPending(ctx) {
		t.Error("pending before the first pass")
	}
	h.once()
	h.src.evs = append(h.src.evs, event(2, events.Info, "boring"))
	if h.run.EventsPending(ctx) {
		t.Error("an event below events_min is pending")
	}
	h.src.evs = append(h.src.evs, event(3, events.Error, "bad"))
	if !h.run.EventsPending(ctx) {
		t.Error("a new error event is not pending")
	}
	h.run.Config.Notifications.Events = "off"
	if h.run.EventsPending(ctx) {
		t.Error("pending with events off")
	}
	h.run.Config.Notifications.Events = "auto"
	h.run.ZED = ZED{Running: true, Channels: []string{"ntfy"}}
	if h.run.EventsPending(ctx) {
		t.Error("pending while ZED sends events")
	}
}

// A pass checks every host: each sends under its own name and keeps
// its own state, a host that cannot be read says so, and the heartbeat
// is requested once.
func TestWatchHosts(t *testing.T) {
	h := newHarness(t)
	h.run.Config.Notifications.HeartbeatURL = "https://hc.example.org/ping/x"
	h.src.pools = []zfs.Overview{overview(t, "tank", "DEGRADED", 10)}
	remote := h.run
	remote.Host, remote.NoHeartbeat = "pve1", true
	remote.Source = &fakeSource{err: errors.New("pve1 could not be reached: ssh: connect to host pve1 port 22: Connection refused")}
	remote.StateDir = filepath.Join(t.TempDir(), "pve1")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var results []Result
	Watch{
		Load: func() ([]Run, []string, error) { return []Run{h.run, remote}, nil, nil },
		Report: func(res Result, _ []string) {
			if results = append(results, res); len(results) == 2 {
				cancel()
			}
		},
		Fail: func(err error) { t.Error(err) },
	}.Loop(ctx, nil)
	if len(results) != 2 || results[0].Host != "nas" || results[1].Host != "pve1" {
		t.Fatalf("results %+v", results)
	}
	if p := results[1].Problems; len(p) != 1 || !strings.Contains(p[0].Text, "pve1 could not be reached") {
		t.Errorf("pve1 problems %+v", p)
	}
	got := titles(h.out.take("phone"))
	if !strings.Contains(got, "nas: ") || !strings.Contains(got, "pve1: ") {
		t.Errorf("phone got %q", got)
	}
	if n := len(h.out.take("heartbeat")); n != 1 {
		t.Errorf("%d heartbeat requests, want 1", n)
	}
}

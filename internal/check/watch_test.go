// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/events"
)

// watchLoop runs Watch.Loop with load; each pass calls then with
// its number, from the loop's goroutine, so then may change the source
// without racing the loop.
func watchLoop(t *testing.T, load func() (Run, []string, error), wake chan struct{}, then func(pass int, cancel func())) (passes int, failed []error) {
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
	load := func() (Run, []string, error) { return h.run, nil, nil }
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
	load := func() (Run, []string, error) {
		loads++
		if loads == 1 {
			wake <- struct{}{}
			return Run{}, nil, errors.New("config unreadable")
		}
		return h.run, nil, nil
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

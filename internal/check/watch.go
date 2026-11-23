// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"context"
	"slices"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// Watch is fstui watch: a check every notifications.watch_interval, and
// one as soon as the kernel holds an event a check would send, looked
// for every notifications.watch_events. The configuration and secrets
// are read again for every pass, so a saved change applies without a
// restart.
type Watch struct {
	// Load is the runs of a pass, one per host checked, with the
	// configuration as it is now, and the problems found in the files.
	Load func() ([]Run, []string, error)
	// Report is told what each run of a pass did; Fail why a pass
	// could not run.
	Report func(Result, []string)
	Fail   func(error)
}

// Loop runs passes until ctx is done. A value on wake (SIGHUP) runs one
// at once.
func (w Watch) Loop(ctx context.Context, wake <-chan struct{}) {
	for ctx.Err() == nil {
		n := config.Default().Notifications
		runs, notices, err := w.Load()
		if err != nil {
			w.Fail(err)
		}
		for _, r := range runs {
			n = r.Config.Notifications
			pass, cancel := context.WithTimeout(ctx, n.CheckTimeout)
			res := r.Once(pass)
			cancel()
			if ctx.Err() != nil {
				return // stopped during the pass: its result is incomplete
			}
			w.Report(res, notices)
		}
		poll := n.WatchEvents
		if err != nil {
			poll = 0
		}
		wait(ctx, wake, n.WatchInterval, poll, func() bool {
			return slices.ContainsFunc(runs, func(r Run) bool { return r.EventsPending(ctx) })
		})
	}
}

// wait returns after interval, when wake receives, when ctx is done, or
// when pending reports true, asked every poll (never when poll is 0).
func wait(ctx context.Context, wake <-chan struct{}, interval, poll time.Duration, pending func() bool) {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	var tick <-chan time.Time
	if poll > 0 {
		t := time.NewTicker(poll)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			return
		case <-timer.C:
			return
		case <-tick:
			if pending() {
				return
			}
		}
	}
}

// EventsPending reports whether the kernel holds an event the next check
// would send: newer than the last one seen, at or above events_min, with
// events on and not left to ZED. Reading the events is cheap next to a
// check, so fstui watch asks often.
func (r Run) EventsPending(ctx context.Context) bool {
	n := r.Config.Notifications
	if n.Events == "off" || n.Events == "auto" && r.ZED.SendsEvents() {
		return false
	}
	state, err := LoadState(r.StateDir)
	if err != nil || state.Boot != r.Boot {
		return false // the next regular pass starts afresh
	}
	evs, _, err := r.Source.Events(ctx)
	if err != nil {
		return false
	}
	least, wanted := eventsLeast(r.Config)
	if !wanted {
		return false
	}
	for _, e := range evs {
		if e.ID > state.LastEvent && e.Severity >= least {
			return true
		}
	}
	return false
}

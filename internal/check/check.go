// SPDX-License-Identifier: GPL-3.0-or-later

// Package check is fstui check: one look at every pool, and the
// notifications that look calls for (docs/configuration.md, Notifications). It
// changes no pool.
package check

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/problem"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/notify"
)

// Source is a storage backend as fstui check sees it: the measures the
// alerts compare, and the kernel's events. need names the metrics the
// active alerts use, so a costly one (I/O waits) is read only when asked.
// The ZFS backend's is health.Source; any other backend gets
// notifications by providing one.
type Source interface {
	Measures(ctx context.Context, now time.Time, need map[string]bool) ([]problem.Measure, error)
	Events(ctx context.Context) ([]events.Event, int, error)
}

// Run is one fstui check.
type Run struct {
	Source   Source
	Config   config.Config
	Secrets  config.Secrets
	StateDir string
	Host     string
	Boot     string // the kernel's boot ID
	ZED      ZED
	Now      func() time.Time
	// Send sends one message; notify.Send, or a fake in tests.
	Send func(context.Context, notify.Channel, notify.Message) error
}

// Result is what a run found and did.
type Result struct {
	Problems []problem.Problem
	// Events is how many kernel events were sent; EventsNote says why
	// none were looked at, if so.
	Events     int
	EventsNote string
	Sent       map[string]int    // items told, by channel
	Failed     map[string]error  // by channel
	Skipped    map[string]string // why a channel was not used
	// Heartbeat is what became of notifications.heartbeat_url: "" when
	// none is set, "sent", or why not.
	Heartbeat string
	StateErr  error
}

// readProblem is the key of the problem "the pools could not be read".
const readProblem = "check:read"

// Once looks at every pool and sends what the rules say.
func (r Run) Once(ctx context.Context) Result {
	now := r.Now()
	res := Result{Sent: map[string]int{}, Failed: map[string]error{}, Skipped: map[string]string{}}
	state, err := LoadState(r.StateDir)
	res.StateErr = err
	state = migrate(state, r.Config)
	evs, lastEvent, note := r.newEvents(ctx, state)
	res.EventsNote = note
	var found []routed
	ms, err := r.Source.Measures(ctx, now, needed(r.Config))
	read := err == nil
	if err != nil {
		found = []routed{{Problem: problem.Problem{Key: readProblem, State: "failed", Severity: problem.Error, Text: "fstui check could not read the pools: " + firstLine(err)}}}
	}
	found = append(found, evaluate(r.Config, ms, evs, r.Boot)...)
	for _, p := range found {
		if p.event {
			res.Events++
		} else {
			res.Problems = append(res.Problems, p.Problem)
		}
	}
	n := r.Config.Notifications
	items := decide(Rules{Repeat: n.Repeat, Resolved: n.Resolved}, n.Channels, state, found, now)
	sent := r.sendAll(ctx, items, &res)
	res.Heartbeat = r.heartbeat(ctx, read)
	next := nextState(state, found, sent, items, now)
	next.Boot, next.LastEvent = r.Boot, lastEvent
	if err := next.Save(r.StateDir); err != nil {
		res.StateErr = errors.Join(res.StateErr, err)
	}
	return res
}

// newEvents are the kernel events after the last one seen, at or above
// the least severity a kernel-events alert sends; the alerts pick theirs
// from them. The first run, or the first after a reboot with no state,
// returns none: the kernel keeps older events, and sending them would
// replay history. ZED is left to send them under "auto".
func (r Run) newEvents(ctx context.Context, state State) (out []events.Event, last uint64, note string) {
	last = state.LastEvent
	if state.Boot != r.Boot {
		last = 0
	}
	n := r.Config.Notifications
	switch {
	case n.Events == "off":
		return nil, last, "kernel events are off (notifications.events)"
	case n.Events == "auto" && r.ZED.SendsEvents():
		return nil, last, "kernel events are left to ZED, which sends them by " + strings.Join(r.ZED.Channels, ", ")
	}
	least, wanted := eventsLeast(r.Config)
	if !wanted {
		return nil, last, "no alert sends kernel events (template kernel-events)"
	}
	evs, _, err := r.Source.Events(ctx)
	if err != nil {
		return nil, last, "kernel events could not be read: " + firstLine(err)
	}
	first := state.Boot == ""
	newest := last
	for _, e := range evs {
		if !first && e.ID > last && e.Severity >= least {
			out = append(out, e)
		}
		newest = max(newest, e.ID)
	}
	if first {
		return nil, newest, "first run: kernel events from before it are not sent"
	}
	return out, newest, ""
}

// sendAll sends each channel its message, at the same time; sent says
// which got through.
func (r Run) sendAll(ctx context.Context, items map[string][]Item, res *Result) map[string]bool {
	sent := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ch := range r.Config.Notifications.Channels {
		its := items[ch.Name]
		if len(its) == 0 {
			continue
		}
		nc, skip := r.channel(ch)
		if skip != "" {
			mu.Lock()
			res.Skipped[ch.Name], sent[ch.Name] = skip, false
			mu.Unlock()
			continue
		}
		wg.Go(func() {
			err := r.Send(ctx, nc, compose(r.Host, its))
			mu.Lock()
			defer mu.Unlock()
			sent[ch.Name] = err == nil
			if err != nil {
				res.Failed[ch.Name] = err
			} else {
				res.Sent[ch.Name] = len(its)
			}
		})
	}
	wg.Wait()
	return sent
}

// channel is the notify channel for ch with its secret, or why it cannot
// be used.
func (r Run) channel(ch config.Channel) (notify.Channel, string) {
	secret, err := r.Secrets.Secret(ch)
	switch {
	case err != nil && !errors.Is(err, config.ErrNoSecret):
		return notify.Channel{}, err.Error()
	case err != nil && notify.NeedsSecret(ch):
		return notify.Channel{}, "no secret: add " + ch.Name + " to secrets.yaml, or set secret_file"
	}
	return notify.FromConfig(ch, secret, r.Config.Notifications), ""
}

// compose is one channel's message: one item as the title itself, several
// counted in the title and listed in the body.
func compose(host string, items []Item) notify.Message {
	level := notify.Info
	var lines []string
	counts := map[Kind]int{}
	for _, it := range items {
		counts[it.Kind]++
		if it.Kind != Resolved {
			level = max(level, notify.Level(it.Severity))
		}
		lines = append(lines, [...]string{"new", "still", "resolved"}[it.Kind]+": "+it.Text)
	}
	if len(items) == 1 {
		title := items[0].Text
		if items[0].Kind == Resolved {
			title = "resolved: " + title
		}
		return notify.Message{Title: host + ": " + title, Body: lines[0], Level: level}
	}
	var parts []string
	for k, word := range [...]string{"new", "still open", "resolved"} {
		if counts[Kind(k)] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[Kind(k)], word))
		}
	}
	return notify.Message{Title: host + ": " + strings.Join(parts, ", "), Body: strings.Join(lines, "\n"), Level: level}
}

func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

// ErrNoChannel is returned by Test for a name no channel has.
var ErrNoChannel = errors.New("no channel by that name in notifications.channels")

// Test sends a test message to the channel named name.
func (r Run) Test(ctx context.Context, name string) error {
	for _, ch := range r.Config.Notifications.Channels {
		if ch.Name == name {
			return notify.Test(ctx, r.Config.Notifications, ch, r.Secrets, r.Host) // notify names the channel
		}
	}
	return fmt.Errorf("%q: %w", name, ErrNoChannel)
}

// heartbeat requests notifications.heartbeat_url when the pools were
// read: a run that could not read them stays silent, so the service
// outside the host warns about it as about a host that stopped.
func (r Run) heartbeat(ctx context.Context, read bool) string {
	n := r.Config.Notifications
	switch {
	case n.HeartbeatURL == "":
		return ""
	case !read:
		return "not sent: the pools could not be read"
	}
	ch := notify.Channel{Name: "heartbeat", Type: "heartbeat", URL: n.HeartbeatURL, AttemptTimeout: n.AttemptTimeout, SendTimeout: n.SendTimeout, Retries: n.Retries}
	if err := r.Send(ctx, ch, notify.Message{Title: "fstui check ran"}); err != nil {
		return "failed: " + err.Error()
	}
	return "sent"
}

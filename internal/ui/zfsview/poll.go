// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// maxBackoff caps the retry delay after failures at this many intervals.
const maxBackoff = 10

// A listing (slow set) may take limits.list_timeout to read: zfs list
// takes about half a second per thousand snapshots. After a read of d, the
// next waits at least limits.slow_read_factor × d, so fstui keeps zfs busy
// a fraction of the time at most.

// poller keeps one source fresh: at most one read in flight,
// the next one scheduled only when it completes, the interval doubled per
// consecutive failure up to maxBackoff, and the last good data kept while
// reads fail. T must be a type no other poller uses: results reach every
// screen and are told apart by their type.
type poller[T any] struct {
	read     func(context.Context) (T, error)
	interval time.Duration
	now      func() time.Time
	// accept, if set, drops results that are not for this poller's
	// subject: a read started for a dataset the user has since left. Read
	// functions used with it name their subject in T even when they fail.
	accept func(T) bool
	// slow marks a listing whose read time grows with the number of
	// datasets or snapshots; it gets longReads instead of the default.
	slow bool
	// limits are limits.list_timeout and slow_read_factor, read when they
	// are used so a saved change applies; nil uses the defaults.
	limits *config.Limits

	data     T
	loaded   time.Time // zero until a read succeeds
	lastErr  termtext.Text
	failures int
	inFlight bool
	took     time.Duration // the last read's duration
	timer    int           // numbers the scheduled poll, so refresh supersedes it
}

type (
	polled[T any] struct {
		data T
		err  error
		at   time.Time
		took time.Duration
	}
	pollDue[T any] struct{ timer int }
)

// start begins a read unless one is in flight.
func (p *poller[T]) start() tea.Cmd {
	if p.inFlight {
		return nil
	}
	p.inFlight = true
	read, now, slow, limit := p.read, p.now, p.slow, p.lim().ListTimeout
	return func() tea.Msg {
		ctx, cancel := context.Background(), context.CancelFunc(func() {})
		if slow {
			ctx, cancel = context.WithTimeout(ctx, limit)
		}
		defer cancel()
		start := time.Now()
		data, err := read(ctx)
		return polled[T]{data: data, err: err, at: now(), took: time.Since(start)}
	}
}

// refresh reads now; the pending timer no longer fires. A read in flight is not interrupted.
func (p *poller[T]) refresh() tea.Cmd {
	p.timer++
	return p.start()
}

// handle consumes this poller's messages. updated reports new data or a
// new error, so the screen can adjust its selection.
func (p *poller[T]) handle(msg tea.Msg) (cmd tea.Cmd, updated bool) {
	switch msg := msg.(type) {
	case polled[T]:
		if p.accept != nil && !p.accept(msg.data) {
			return nil, false
		}
		p.inFlight, p.took = false, msg.took
		if msg.err != nil {
			p.failures++
			p.lastErr = errText(msg.err)
		} else {
			p.failures, p.lastErr = 0, termtext.Text{}
			p.data, p.loaded = msg.data, msg.at
		}
		p.timer++
		timer := p.timer
		return tea.Tick(p.delay(), func(time.Time) tea.Msg { return pollDue[T]{timer: timer} }), true
	case pollDue[T]:
		if msg.timer == p.timer {
			return p.start(), false
		}
		return nil, false
	}
	return nil, false
}

func (p *poller[T]) delay() time.Duration {
	limit := maxBackoff * p.interval
	d := p.interval
	for i := 0; i < p.failures && d < limit; i++ {
		d *= 2
	}
	return max(min(d, limit), time.Duration(p.lim().SlowReadFactor)*p.took)
}

func (p *poller[T]) lim() config.Limits {
	if p.limits == nil {
		return config.Default().Limits
	}
	return *p.limits
}

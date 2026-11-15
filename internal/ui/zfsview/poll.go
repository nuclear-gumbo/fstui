// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// maxBackoff caps the retry delay after failures at this many intervals.
const maxBackoff = 10

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

	data     T
	loaded   time.Time // zero until a read succeeds
	lastErr  termtext.Text
	failures int
	inFlight bool
	timer    int // numbers the scheduled poll, so refresh supersedes it
}

type (
	polled[T any] struct {
		data T
		err  error
		at   time.Time
	}
	pollDue[T any] struct{ timer int }
)

// start begins a read unless one is in flight.
func (p *poller[T]) start() tea.Cmd {
	if p.inFlight {
		return nil
	}
	p.inFlight = true
	read, now := p.read, p.now
	return func() tea.Msg {
		data, err := read(context.Background())
		return polled[T]{data: data, err: err, at: now()}
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
		p.inFlight = false
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
	return min(d, limit)
}

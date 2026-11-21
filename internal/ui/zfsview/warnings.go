// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"time"

	"github.com/nuclear-gumbo/fstui/internal/problem"

	"github.com/nuclear-gumbo/fstui/internal/zfs/health"

	"github.com/nuclear-gumbo/fstui/internal/format"
)

// warnings are the problems of the pools shown, as the health package
// finds them, with whether a scrub is coming added to an overdue one.
func (p *Pools) warnings() []problem.Problem {
	now := p.opts.Now()
	return health.Check(p.poll.data, health.Options{
		Thresholds: p.opts.Thresholds, Version: p.opts.Version, Now: now,
		ScrubNote: func(pool string) string { return p.scheduleNote(pool, now) },
	})
}

// visibleWarnings drops advisory warnings in superuser mode and those the
// user hid this session. A hidden warning returns when its state changes,
// for example when a pool crosses from WARN to FULL.
func (p *Pools) visibleWarnings() []problem.Problem {
	var out []problem.Problem
	for _, w := range p.warnings() {
		if w.Advisory && (p.opts.Superuser || p.isHidden(w)) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// hiddenCount is the number of warnings hidden with x that still apply.
func (p *Pools) hiddenCount() int {
	n := 0
	for _, w := range p.warnings() {
		if w.Advisory && !p.opts.Superuser && p.isHidden(w) {
			n++
		}
	}
	return n
}

func (p *Pools) isHidden(w problem.Problem) bool {
	state, ok := p.hidden[w.Key]
	return ok && state == w.State
}

// scheduleNote completes an overdue-scrub warning with whether a scrub is
// coming: that is the difference between "wait" and "set one up".
func (p *Pools) scheduleNote(pool string, now time.Time) string {
	s, scheduled, known := p.schedule(pool)
	switch {
	case !known:
		return ""
	case !scheduled:
		return "; no scrub is scheduled"
	case s.Next.IsZero():
		return "; one is scheduled"
	}
	return "; the next is in " + format.Age(s.Next.Sub(now))
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// checkpointAgeWarn: a checkpoint older than this is probably forgotten
// and is slowly holding on to every deleted block.
const checkpointAgeWarn = 24 * time.Hour

// warning is one line under the pool table.
type warning struct {
	id string // stable across refreshes, e.g. "capacity:tank"
	// state is what must change for a hidden warning to return: the
	// threshold crossed, not the exact figure, so a pool that goes from
	// 93% to 94% full stays hidden but one that becomes FULL does not.
	state string
	text  string
	// advisory warnings can be hidden for the session with x and are not
	// shown in superuser mode. Health problems and data errors are state,
	// not advice, and are always shown.
	advisory bool
}

func (p *Pools) warnings() []warning {
	var ws []warning
	if v := p.opts.Version; v.Mismatched() {
		text := fmt.Sprintf("Userland %s, kernel module %s. Reboot or reload the module so they match.",
			termtext.New(v.Userland).Display(), termtext.New(v.Kernel).Display())
		ws = append(ws, warning{"version", v.Userland + " " + v.Kernel, text, true})
	}
	now := p.opts.Now()
	for _, o := range p.poll.data {
		ws = append(ws, p.poolWarnings(o, now)...)
	}
	return ws
}

func (p *Pools) poolWarnings(o zfs.Overview, now time.Time) []warning {
	name := o.Pool.Name.String()
	var ws []warning
	if o.Status.State != "ONLINE" {
		ws = append(ws, warning{"health:" + name, "", fmt.Sprintf("%s is %s%s", name, o.Status.State, deviceProblems(o.Status)), false})
	}
	if n := o.Status.ErrorCount; n > 0 {
		ws = append(ws, warning{"data:" + name, "", fmt.Sprintf("%s has %d data errors; zpool status -v %s lists the affected files", name, n, name), false})
	}
	th := p.opts.Thresholds
	if pct := capacityPct(o); ui.LevelOf(pct, th.CapacityWarn, th.CapacityError) != ui.Normal {
		level := ui.LevelWord(ui.LevelOf(pct, th.CapacityWarn, th.CapacityError))
		ws = append(ws, warning{"capacity:" + name, level, fmt.Sprintf("%s is %d%% full (warning from %d%%)", name, pct, th.CapacityWarn), true})
	}
	if frag, ok := o.Pool.Properties.Number("fragmentation"); ok && int(frag) >= th.FragmentationWarn { //nolint:gosec // a percentage
		ws = append(ws, warning{"fragmentation:" + name, "over", fmt.Sprintf("free space in %s is %d%% fragmented; writes may slow down", name, frag), true})
	}
	if sc := o.Status.Scan; sc != nil && sc.Function == "SCRUB" && !sc.End.IsZero() {
		if age := now.Sub(sc.End); age > time.Duration(th.ScrubOverdueDays)*24*time.Hour {
			text := fmt.Sprintf("%s was last scrubbed %s ago (overdue after %d days)", name, ui.Age(age), th.ScrubOverdueDays)
			ws = append(ws, warning{"scrub:" + name, sc.End.String(), text + p.scheduleNote(name, now), true})
		}
	}
	if c := o.Status.Checkpoint; c != nil && now.Sub(c.Taken) > checkpointAgeWarn {
		ws = append(ws, warning{"checkpoint:" + name, c.Taken.String(), fmt.Sprintf("%s has held a checkpoint for %s (%s); remove, attach, detach, split and reguid are blocked until it is discarded",
			name, ui.Age(now.Sub(c.Taken)), ui.Size(c.Space, false)), true})
	}
	return ws
}

// deviceProblems summarises the devices that are not ONLINE, e.g.
// ": 1 device OFFLINE".
func deviceProblems(st zfs.PoolStatus) string {
	counts := map[string]int{}
	st.Walk(func(v zfs.Vdev) {
		if len(v.Children) == 0 && v.State != "ONLINE" && v.State != "AVAIL" && v.State != "INUSE" {
			counts[v.State]++
		}
	})
	if len(counts) == 0 {
		return ""
	}
	states := make([]string, 0, len(counts))
	for s := range counts {
		states = append(states, s)
	}
	slices.Sort(states)
	parts := make([]string, len(states))
	for i, s := range states {
		parts[i] = fmt.Sprintf("%d %s %s", counts[s], plural(counts[s], "device", "devices"), s)
	}
	return ": " + strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// visibleWarnings drops advisory warnings in superuser mode and those the
// user hid this session. A hidden warning returns when its state changes,
// for example when a pool crosses from WARN to FULL.
func (p *Pools) visibleWarnings() []warning {
	var out []warning
	for _, w := range p.warnings() {
		if w.advisory && (p.opts.Superuser || p.isHidden(w)) {
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
		if w.advisory && !p.opts.Superuser && p.isHidden(w) {
			n++
		}
	}
	return n
}

func (p *Pools) isHidden(w warning) bool {
	state, ok := p.hidden[w.id]
	return ok && state == w.state
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
	return "; the next is in " + ui.Age(s.Next.Sub(now))
}

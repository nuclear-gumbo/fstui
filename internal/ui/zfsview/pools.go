// SPDX-License-Identifier: GPL-3.0-or-later

// Package zfsview holds the views for ZFS concepts that have no
// backend-neutral meaning: fragmentation, dedup, scrubs, checkpoints, the
// vdev tree.
package zfsview

import (
	"context"
	"errors"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Source reads pools. *zfs.Reader implements it; tests use a fake, the
// only place fstui is tested with stand-ins for zfs.
type Source interface {
	Overviews(ctx context.Context) ([]zfs.Overview, error)
	PoolProperties(ctx context.Context, pool name.Pool) (zfs.Pool, error)
	PoolHistory(ctx context.Context, pool name.Pool) ([]zfs.HistoryEntry, error)
	PoolDatasets(ctx context.Context, pool name.Pool) ([]zfs.Dataset, error)
}

// PoolsOptions configure the Pools view.
type PoolsOptions struct {
	Interval        time.Duration
	DatasetInterval time.Duration // the Space tab's dataset listing
	Thresholds      config.Thresholds
	Superuser       bool
	Version         zfs.Version // for the userland/kernel mismatch warning
	Root            bool        // zpool history and events need root
	Now             func() time.Time
}

// Pools is the overview of every imported pool.
type Pools struct {
	src    Source
	opts   PoolsOptions
	poll   poller[[]zfs.Overview]
	cursor int
	first  int                 // pool shown in the top row by the last render
	hidden map[string]string   // warning ID -> state hidden for this session
	detail *detail             // the pool whose detail is shown, if any
	focus  bool                // keys go to the detail rather than the list
	geo    geometry            // layout of the last render, for the mouse
	sched  *poller[scrubPlans] // nil when the source cannot tell
}

// ScheduleSource says when pools are scrubbed next. *zfs.Reader
// implements it; a Source without it shows no schedules.
type ScheduleSource interface {
	ScrubSchedules(ctx context.Context, pools []zfs.Overview) (map[string]zfs.ScrubSchedule, error)
}

// scrubPlans is a read of the scrub schedules, by pool name.
type scrubPlans map[string]zfs.ScrubSchedule

// scheduleInterval: timers and cron files change rarely.
const scheduleInterval = time.Minute

// geometry is where the last render put the panels.
type geometry struct {
	split bool // list and detail side by side
	left  int  // width of the list panel when split
	width int  // width of the whole view
}

// detail returns the detail panel's left edge and width.
func (g geometry) detail() (left, width int) {
	if g.split {
		return g.left, g.width - g.left
	}
	return 0, g.width
}

// NewPools returns the Pools view reading from src.
func NewPools(src Source, o PoolsOptions) *Pools {
	p := &Pools{
		src: src, opts: o, hidden: map[string]string{},
		poll: poller[[]zfs.Overview]{read: src.Overviews, interval: o.Interval, now: o.Now},
	}
	if ss, ok := src.(ScheduleSource); ok {
		// The read lists the pools itself: it runs beside the UI and must
		// not touch the view's data.
		read := func(ctx context.Context) (scrubPlans, error) {
			ov, err := src.Overviews(ctx)
			if err != nil {
				return nil, err
			}
			return ss.ScrubSchedules(ctx, ov)
		}
		p.sched = &poller[scrubPlans]{read: read, interval: scheduleInterval, now: o.Now}
	}
	return p
}

// Name implements ui.Screen.
func (p *Pools) Name() string { return "Pools" }

// View numbers of the screen map. Every view names its own, so
// the order of the view bar (the dashboard first) does not renumber them.
const (
	poolsView     = 1
	datasetsView  = 2
	snapshotsView = 3
	volumesView   = 4
)

// Number implements ui.Numbered.
func (p *Pools) Number() int { return poolsView }

// Framed implements ui.Framed: Pools draws its own panels, side by side
// from splitWidth columns.
func (p *Pools) Framed() bool { return true }

// Keys implements ui.Screen.
func (p *Pools) Keys() []key.Binding {
	if p.focus {
		down, up := keys.As(keys.Down, "scroll"), keys.As(keys.Up, "scroll back")
		if p.detail.tab == tabTopology {
			down, up = keys.As(keys.Down, "next device"), keys.As(keys.Up, "previous device")
		}
		bs := []key.Binding{down, up, keys.As(keys.Right, "next tab"), keys.As(keys.Left, "previous tab"), keys.Get(keys.Refresh), keys.As(keys.Back, "back")}
		if p.detail.tab == tabHistory {
			bs = append([]key.Binding{keys.Get(keys.Internal)}, bs...)
		}
		return bs
	}
	return []key.Binding{keys.As(keys.Select, "open pool"), keys.As(keys.Down, "next pool"), keys.As(keys.Up, "previous pool"), keys.Get(keys.Refresh), keys.Get(keys.Hide)}
}

// Nested implements ui.Screen: esc and q leave the detail for the list.
func (p *Pools) Nested() bool { return p.focus }

// Init starts the first load.
func (p *Pools) Init() tea.Cmd {
	if p.sched != nil {
		return tea.Batch(p.poll.start(), p.sched.start())
	}
	return p.poll.start()
}

// reads handles the results and timers of the view's reads.
func (p *Pools) reads(msg tea.Msg) (tea.Cmd, bool) {
	if p.sched != nil {
		if cmd, updated := p.sched.handle(msg); cmd != nil || updated {
			return cmd, true
		}
	}
	if cmd, updated := p.poll.handle(msg); cmd != nil || updated {
		p.cursor = min(p.cursor, max(len(p.poll.data)-1, 0))
		if p.geo.split {
			cmd = tea.Batch(cmd, p.follow())
		}
		return cmd, true
	}
	if d := p.detail; d != nil {
		if cmd, updated := d.space.handle(msg); cmd != nil || updated {
			return cmd, true
		}
	}
	return nil, false
}

// schedule returns a pool's scrub schedule; known is false until the
// schedules have been read (or when they cannot be).
func (p *Pools) schedule(pool string) (s zfs.ScrubSchedule, scheduled, known bool) {
	if p.sched == nil || p.sched.loaded.IsZero() {
		return s, false, false
	}
	s, scheduled = p.sched.data[pool]
	// A time already past (a stale timer, a clock that moved) says only
	// that a scrub is scheduled.
	if s.Next.Before(p.opts.Now()) {
		s.Next = time.Time{}
	}
	return s, scheduled, true
}

// Update implements ui.Screen.
func (p *Pools) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	if cmd, handled := p.reads(msg); handled {
		return p, cmd
	}
	if cmd, ok := p.mouse(msg); ok {
		return p, cmd
	}
	if _, ok := msg.(ui.ChangedMsg); ok {
		return p, p.poll.refresh() // a scrub or clear shows at once
	}
	if c, ok := msg.(ui.ConfigMsg); ok {
		p.opts.Thresholds, p.opts.Superuser = c.Config.Thresholds, c.Config.Superuser
		p.opts.Interval, p.opts.DatasetInterval = c.Config.Refresh.Pools, c.Config.Refresh.Datasets
		p.poll.interval = c.Config.Refresh.Pools
		return p, nil
	}
	switch msg := msg.(type) {
	case propsMsg:
		return p, p.applyProps(msg)
	case historyMsg:
		p.applyHistory(msg)
	case propsPollMsg:
		if d := p.detail; d != nil && d.pool == msg.pool && d.timer == msg.timer {
			return p, p.loadProps()
		}
	case tea.KeyPressMsg:
		if p.focus {
			return p, p.detailKey(msg)
		}
		cmd := p.key(msg)
		if p.geo.split && !p.focus {
			cmd = tea.Batch(cmd, p.follow())
		}
		return p, cmd
	}
	return p, nil
}

func (p *Pools) key(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.As(keys.Select, "open pool")):
		return p.open()
	case key.Matches(msg, keys.As(keys.Down, "next pool")):
		p.cursor = min(p.cursor+1, max(len(p.poll.data)-1, 0))
	case key.Matches(msg, keys.As(keys.Up, "previous pool")):
		p.cursor = max(p.cursor-1, 0)
	case key.Matches(msg, keys.Get(keys.Refresh)):
		return p.poll.refresh()
	case key.Matches(msg, keys.Get(keys.Hide)):
		for _, w := range p.visibleWarnings() {
			if w.advisory {
				p.hidden[w.id] = w.state
			}
		}
	}
	return nil
}

// errText keeps zpool's stderr when the read failed with one; it says
// what went wrong in ZFS's own words.
func errText(err error) termtext.Text {
	var ce *execute.CommandError
	if errors.As(err, &ce) && !ce.Stderr.IsEmpty() {
		return ce.Stderr
	}
	return termtext.New(err.Error())
}

// Status implements ui.Screen.
func (p *Pools) Status() ui.Status {
	return ui.Status{Refreshed: p.poll.loaded, LastError: p.poll.lastErr, Figure: p.figure()}
}

// mouse handles the wheel and clicks. The wheel moves the selection or
// scrolls the detail, whichever has focus. A click in the list selects a
// pool, a second click opens it; a click in the detail focuses it, and
// on its top edge switches to the tab under the pointer.
func (p *Pools) mouse(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case ui.ScrollMsg:
		if p.focus {
			p.detail.scroll = max(p.detail.scroll+msg.Delta, 0)
			return nil, true
		}
		p.cursor = max(min(p.cursor+msg.Delta, len(p.poll.data)-1), 0)
		if p.geo.split {
			return p.follow(), true
		}
		return nil, true
	case ui.ClickMsg:
		return p.click(msg), true
	}
	return nil, false
}

func (p *Pools) click(msg ui.ClickMsg) tea.Cmd {
	inDetail := p.focus && !p.geo.split || p.geo.split && msg.X >= p.geo.left
	if inDetail && p.detail != nil {
		left, w := p.geo.detail()
		p.focus = true
		if t := p.detailFrame(true).TabAt(w, msg.X-left); msg.Y == 0 && t >= 0 {
			p.detail.tab, p.detail.scroll = t, 0
			return p.enterTab()
		}
		return nil
	}
	p.focus = false
	// The list's first pool row is below the panel's top edge and the
	// column header.
	row := p.first + msg.Y - listTop
	if msg.Y < listTop || row >= len(p.poll.data) {
		return nil
	}
	if row == p.cursor {
		return p.open() // a second click opens, like enter
	}
	p.cursor = row
	if p.geo.split {
		return p.follow()
	}
	return nil
}

// listTop is the screen row of the first pool: the panel edge, then the
// column header.
const listTop = 2

// Selection implements ui.Selector: the device selected in Topology
// while the pool's detail has the focus, otherwise the pool under the
// cursor.
func (p *Pools) Selection() (ui.Target, bool) {
	if len(p.poll.data) == 0 && !p.poll.loaded.IsZero() && p.poll.lastErr.IsEmpty() {
		// No pool imported: a new one can still be made.
		return ui.Target{Backend: "zfs", Kind: "pools", Names: []string{"pools"}}, true
	}
	if p.cursor >= len(p.poll.data) {
		return ui.Target{}, false
	}
	o := p.poll.data[p.cursor]
	pool := o.Pool.Name.String()
	if p.focus && p.detail != nil && p.detail.tab == tabTopology {
		if leaves := leafNames(o.Status); len(leaves) > 0 {
			dev := leaves[min(p.detail.device, len(leaves)-1)]
			return ui.Target{Backend: "zfs", Kind: "device", Names: []string{dev}, Parent: pool}, true
		}
	}
	return ui.Target{Backend: "zfs", Kind: "pool", Names: []string{pool}}, true
}

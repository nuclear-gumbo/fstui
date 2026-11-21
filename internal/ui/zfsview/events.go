// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

const (
	eventsView = 7
	// Polling rates: zpool events is read every 2 seconds while the view is
	// shown, every 30 otherwise.
)

// EventSource reads every event the kernel holds. *zfs.Reader
// implements it.
type EventSource interface {
	Events(ctx context.Context) ([]events.Event, int, error)
}

type eventRead struct {
	evs     []events.Event
	dropped int
}

// EventsOptions configures the Events view.
type EventsOptions struct {
	Root      bool // zpool events needs root
	Retention int
	Filters   []config.EventFilter
	Now       func() time.Time
	Settings  *config.Config // shared by the views; nil: defaults
}

// Events lists what the kernel reported (view 7): live, newest first,
// filterable.
type Events struct {
	poll poller[eventRead]
	opts EventsOptions
	log  *events.Log

	filter eventFilter
	saved  int     // index of the active saved filter, -1 for none
	input  *string // the text filter while it is typed
	before string  // the text filter before typing started

	follow   bool   // keep the newest event selected
	unseen   int    // events that arrived while paused
	cursor   int    // index into shown()
	pinned   uint64 // ID of the selected event, kept selected while paused
	first    int    // first row drawn, for the mouse
	focus    bool   // the detail has the focus
	onScreen bool   // the view is shown, which sets the read interval
	geo      geometry
}

// NewEvents returns the Events view.
func NewEvents(src EventSource, o EventsOptions) *Events {
	read := func(ctx context.Context) (eventRead, error) {
		evs, dropped, err := src.Events(ctx)
		return eventRead{evs, dropped}, err
	}
	o.Settings = settingsOr(o.Settings)
	return &Events{
		poll:   poller[eventRead]{read: read, interval: o.Settings.Refresh.EventsHidden, now: o.Now},
		opts:   o,
		log:    events.NewLog(o.Retention),
		saved:  -1,
		follow: true,
	}
}

// Name implements ui.Screen.
func (v *Events) Name() string { return "Events" }

// Number implements ui.Numbered.
func (v *Events) Number() int { return eventsView }

// Nested implements ui.Screen: esc leaves the detail or the filter first.
func (v *Events) Nested() bool { return v.focus || v.input != nil }

// Typing implements ui.Typing while the text filter is typed.
func (v *Events) Typing() bool { return v.input != nil }

// Init implements ui.Screen. Without root there is nothing to read.
func (v *Events) Init() tea.Cmd {
	if !v.opts.Root {
		return nil
	}
	return v.poll.start()
}

// Activate implements ui.Activator.
func (v *Events) Activate() tea.Cmd {
	if !v.opts.Root {
		return nil
	}
	v.onScreen = true
	v.poll.interval = v.opts.Settings.Refresh.Events
	return v.poll.refresh()
}

// Deactivate implements ui.Deactivator.
func (v *Events) Deactivate() tea.Cmd {
	v.onScreen = false
	v.poll.interval = v.opts.Settings.Refresh.EventsHidden
	return nil
}

// Status implements ui.Screen.
func (v *Events) Status() ui.Status {
	st := ui.Status{Refreshed: v.poll.loaded, LastError: v.poll.lastErr, Static: !v.opts.Root}
	var errs, warns int
	for _, e := range v.log.Events() {
		switch e.Severity {
		case events.Error:
			errs++
		case events.Warn:
			warns++
		case events.Info, events.Notice:
		}
	}
	st.Figure = countWords(errs, "error", "errors") + countWords(warns, "warning", "warnings")
	return st
}

func countWords(n int, one, many string) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1 " + one + " "
	}
	return strconv.Itoa(n) + " " + many + " "
}

// Keys implements ui.Screen.
func (v *Events) Keys() []key.Binding {
	switch {
	case v.input != nil:
		return []key.Binding{keys.As(keys.Select, "apply"), cancelText()}
	case v.focus:
		return []key.Binding{keys.As(keys.Back, "back to the list")}
	}
	return []key.Binding{
		keys.As(keys.Select, "details"), keys.Get(keys.Filter), keys.Get(keys.FilterSeverity),
		keys.Get(keys.FilterPool), keys.Get(keys.FilterClass), keys.Get(keys.SavedFilter), keys.Get(keys.Follow),
		keys.As(keys.Down, "older"), keys.As(keys.Up, "newer"), keys.Get(keys.Refresh),
	}
}

// cancelText is Back without q, which is text while typing.
func cancelText() key.Binding {
	ks := slices.DeleteFunc(slices.Clone(keys.Get(keys.Back).Keys()), func(k string) bool { return k == "q" })
	return key.NewBinding(key.WithKeys(ks...), key.WithHelp(strings.Join(ks, "/"), "cancel"))
}

// Update implements ui.Screen.
func (v *Events) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	if cmd, updated := v.poll.handle(msg); cmd != nil || updated {
		// updated is also true for a failed read, which leaves the data
		// as it was.
		if updated && v.poll.lastErr.IsEmpty() {
			v.record(v.poll.data)
		}
		return v, cmd
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return v, v.key(msg)
	case ui.ConfigMsg:
		v.opts.Filters = msg.Config.Events.Filters
		v.log.SetRetention(msg.Config.Events.Retention)
		v.poll.interval = v.opts.Settings.Refresh.EventsHidden
		if v.onScreen {
			v.poll.interval = v.opts.Settings.Refresh.Events
		}
	case ui.ScrollMsg:
		v.move(v.cursor + msg.Delta)
	case ui.ClickMsg:
		v.click(msg)
	}
	return v, nil
}

// record adds a read to the log. Following, the newest event is
// selected; paused, the selected event stays selected and new ones that
// pass the filter are counted.
func (v *Events) record(r eventRead) {
	fresh := v.log.Add("zfs", r.evs, r.dropped)
	if v.follow {
		v.cursor = 0
		return
	}
	for _, e := range fresh {
		if v.filter.matches(e) {
			v.unseen++
		}
	}
	v.reselect()
}

// reselect finds the pinned event again after the list changed.
func (v *Events) reselect() {
	shown := v.shown()
	if i := slices.IndexFunc(shown, func(e events.Event) bool { return e.ID == v.pinned }); i >= 0 {
		v.cursor = i
	}
	v.cursor = max(min(v.cursor, len(shown)-1), 0)
}

// shown is the kept events that pass the filter, newest first.
func (v *Events) shown() []events.Event {
	all := v.log.Events()
	out := make([]events.Event, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		if v.filter.matches(all[i]) {
			out = append(out, all[i])
		}
	}
	return out
}

// move selects row i; leaving the newest row pauses following.
func (v *Events) move(i int) {
	shown := v.shown()
	v.cursor = max(min(i, len(shown)-1), 0)
	if v.cursor > 0 {
		v.follow = false
	}
	if v.cursor < len(shown) {
		v.pinned = shown[v.cursor].ID
	}
}

// showEvent selects the event with this ID and opens its detail, for the
// dashboard. The filter is cleared if it hides the event.
func (v *Events) showEvent(id uint64) {
	if !slices.ContainsFunc(v.shown(), func(e events.Event) bool { return e.ID == id }) {
		v.filter, v.saved = eventFilter{}, -1
	}
	v.follow, v.unseen, v.pinned = false, 0, id
	v.reselect()
	v.focus = true
}

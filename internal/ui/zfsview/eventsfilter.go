// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// eventFilter selects events. Empty fields match everything.
type eventFilter struct {
	name  string            // the saved filter it came from, if any
	sevs  []events.Severity // a saved filter's severities
	min   events.Severity   // the minimum chosen with v
	pool  string
	class string // part of the class
	text  string // part of any field, ignoring case
}

func (f eventFilter) matches(e events.Event) bool {
	switch {
	case len(f.sevs) > 0 && !slices.Contains(f.sevs, e.Severity),
		e.Severity < f.min,
		f.pool != "" && e.Source.Raw() != f.pool,
		f.class != "" && !strings.Contains(e.Class.Raw(), f.class):
		return false
	case f.text == "":
		return true
	}
	t := strings.ToLower(f.text)
	for _, s := range []string{e.Summary.Raw(), e.Source.Raw(), e.Device.Raw(), e.Class.Raw()} {
		if strings.Contains(strings.ToLower(s), t) {
			return true
		}
	}
	return slices.ContainsFunc(e.Detail, func(d events.Field) bool {
		return strings.Contains(strings.ToLower(d.Value.Raw()), t)
	})
}

// describe is the active filter as the header shows it.
func (f eventFilter) describe() string {
	var parts []string
	if f.name != "" {
		parts = append(parts, "saved: "+f.name)
	}
	if len(f.sevs) > 0 {
		words := make([]string, len(f.sevs))
		for i, s := range f.sevs {
			words[i] = s.String()
		}
		parts = append(parts, "severity "+strings.Join(words, ","))
	}
	if f.min > events.Info {
		parts = append(parts, "severity >= "+f.min.String())
	}
	for _, p := range [][2]string{{"pool", f.pool}, {"class", f.class}, {"text", f.text}} {
		if p[1] != "" {
			parts = append(parts, p[0]+" "+p[1])
		}
	}
	if len(parts) == 0 {
		return "all events"
	}
	return strings.Join(parts, ", ")
}

func savedFilter(c config.EventFilter) eventFilter {
	f := eventFilter{name: c.Name, pool: c.Pool, class: c.Class, text: c.Text}
	for _, s := range c.Severities {
		f.sevs = append(f.sevs, events.Severity(slices.Index(config.SeverityNames, s)+1))
	}
	return f
}

// key handles a key in the list, the detail or the text filter.
func (v *Events) key(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case v.input != nil:
		v.typeKey(msg)
		return nil
	case v.focus:
		if key.Matches(msg, keys.Get(keys.Back)) {
			v.focus = false
		}
		return nil
	case key.Matches(msg, keys.Get(keys.Refresh)):
		return v.poll.refresh()
	}
	v.listKey(msg)
	return nil
}

func (v *Events) listKey(msg tea.KeyPressMsg) {
	f := &v.filter
	switch {
	case key.Matches(msg, keys.As(keys.Down, "older")):
		v.move(v.cursor + 1)
	case key.Matches(msg, keys.As(keys.Up, "newer")):
		v.move(v.cursor - 1)
	case key.Matches(msg, keys.Get(keys.First)):
		v.move(0)
	case key.Matches(msg, keys.Get(keys.Last)):
		v.move(len(v.shown()) - 1)
	case key.Matches(msg, keys.Get(keys.Select)):
		v.focus = len(v.shown()) > 0
	case key.Matches(msg, keys.Get(keys.Follow)):
		v.follow, v.unseen = !v.follow, 0
		if v.follow {
			v.move(0)
		}
	case key.Matches(msg, keys.Get(keys.Filter)):
		v.before = f.text
		text := f.text
		v.input = &text
	case key.Matches(msg, keys.Get(keys.FilterSeverity)):
		// All, then NOTICE and up, WARN and up, ERROR, and all again.
		f.name, f.sevs = "", nil
		switch f.min {
		case 0, events.Info:
			f.min = events.Notice
		case events.Notice, events.Warn:
			f.min++
		case events.Error:
			f.min = 0
		}
		v.refiltered()
	case key.Matches(msg, keys.Get(keys.FilterPool)):
		f.name, f.pool = "", nextValue(v.values(func(e events.Event) string { return e.Source.Raw() }), f.pool)
		v.refiltered()
	case key.Matches(msg, keys.Get(keys.FilterClass)):
		f.name, f.class = "", nextValue(v.values(shortClass), f.class)
		v.refiltered()
	case key.Matches(msg, keys.Get(keys.SavedFilter)):
		v.nextSaved()
	}
}

// typeKey edits the text filter, which applies as it is typed.
func (v *Events) typeKey(msg tea.KeyPressMsg) {
	switch {
	case key.Matches(msg, cancelText()):
		v.filter.text, v.input = v.before, nil
	case msg.String() == "enter":
		v.input = nil
	case msg.String() == "backspace":
		if t := *v.input; t != "" {
			*v.input = t[:len(t)-1]
		}
	case msg.Text != "":
		*v.input += msg.Text
	default:
		return
	}
	if v.input != nil {
		v.filter.text, v.filter.name = *v.input, ""
	}
	v.refiltered()
}

func (v *Events) nextSaved() {
	v.saved++
	if v.saved >= len(v.opts.Filters) {
		v.saved, v.filter = -1, eventFilter{}
	} else {
		v.filter = savedFilter(v.opts.Filters[v.saved])
	}
	v.refiltered()
}

// refiltered keeps the selection sensible after the filter changed.
func (v *Events) refiltered() {
	v.unseen = 0
	if v.follow {
		v.cursor = 0
		return
	}
	v.reselect()
}

// values lists the distinct values of one property of the kept events.
func (v *Events) values(of func(events.Event) string) []string {
	var out []string
	for _, e := range v.log.Events() {
		if s := of(e); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

// nextValue cycles through "" (everything) and then each value.
func nextValue(values []string, cur string) string {
	i := slices.Index(values, cur)
	if i+1 >= len(values) {
		return ""
	}
	return values[i+1]
}

// shortClass is the last part of a class: checksum, scrub_finish.
func shortClass(e events.Event) string {
	c := e.Class.Raw()
	return c[strings.LastIndex(c, ".")+1:]
}

func (v *Events) click(msg ui.ClickMsg) {
	left, _ := v.geo.detail()
	if v.geo.split && msg.X >= left {
		v.focus = len(v.shown()) > 0
		return
	}
	v.focus = false
	if row := v.first + msg.Y - eventsListTop; msg.Y >= eventsListTop {
		v.move(row)
	}
}

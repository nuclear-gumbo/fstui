// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/problem"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// Kind is why an item is in a message.
type Kind int

// Kinds.
const (
	New Kind = iota
	Again
	Resolved
)

// Item is one line of a message to a channel.
type Item struct {
	Kind     Kind
	Key      string
	Severity problem.Severity
	Text     string
}

// Rules are the notifications: settings that decide what is sent.
type Rules struct {
	Repeat   time.Duration
	Resolved bool
}

// decide compares the problems the alerts found now with the state of
// the last run and says what each channel should be told. A problem is
// new to a channel if it was not open last time, its state changed or its
// severity rose; it is sent again after its alert's repeat; one that is
// gone is sent as resolved to the channels that heard of it. Kernel
// events are sent once. A channel hears only the alerts that name it, or
// name no channel, at or above its least severity.
func decide(r Rules, channels []config.Channel, prev State, problems []routed, now time.Time) map[string][]Item {
	out := map[string][]Item{}
	for _, ch := range channels {
		items := openItems(r, ch, prev, problems, now)
		items = append(items, resolvedItems(r, ch, prev, problems)...)
		if len(items) > 0 {
			slices.SortStableFunc(items, func(a, b Item) int { return int(a.Kind) - int(b.Kind) })
			out[ch.Name] = items
		}
	}
	return out
}

// openItems are the open problems and new events ch hears about now:
// new ones, and those due again.
func openItems(r Rules, ch config.Channel, prev State, problems []routed, now time.Time) []Item {
	least := severityOf(ch.MinSeverity)
	var out []Item
	for _, p := range problems {
		if p.Severity < least || !p.heardBy(ch.Name) {
			continue
		}
		if p.event {
			out = append(out, Item{New, p.Key, p.Severity, p.Text})
			continue
		}
		repeat := r.Repeat
		if p.repeat != nil {
			repeat = *p.repeat
		}
		e := prev.Problems[p.Key]
		switch {
		case e == nil || e.State != p.State || severityOf(e.Severity) < p.Severity || e.Sent[ch.Name].IsZero():
			out = append(out, Item{New, p.Key, p.Severity, p.Text})
		case repeat > 0 && now.Sub(e.Sent[ch.Name]) >= repeat:
			out = append(out, Item{Again, p.Key, p.Severity, p.Text})
		}
	}
	return out
}

// resolvedItems are the problems ch heard of that are gone now, when
// their alert sends resolutions.
func resolvedItems(r Rules, ch config.Channel, prev State, problems []routed) []Item {
	var out []Item
	for key, e := range prev.Problems {
		open := slices.ContainsFunc(problems, func(p routed) bool { return p.Key == key })
		send := r.Resolved
		if e.Resolved != nil {
			send = *e.Resolved
		}
		if open || !send || e.Sent[ch.Name].IsZero() {
			continue
		}
		text := e.ResolvedText
		if text == "" {
			text = resolvedText(key)
		}
		out = append(out, Item{Resolved, key, severityOf(e.Severity), text})
	}
	slices.SortFunc(out, func(a, b Item) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// nextState is the state after this run: the open problems, keeping when
// each channel was told, with sent marking the channels whose message got
// through now. A resolved problem a channel could not be told about stays,
// for that channel only, so the next run tells it. Kernel events are not
// kept: the last event ID stands for them.
func nextState(prev State, problems []routed, sent map[string]bool, items map[string][]Item, now time.Time) State {
	next := State{Version: stateVersion, Boot: prev.Boot, LastEvent: prev.LastEvent, Problems: map[string]*Entry{}}
	for _, p := range problems {
		if p.event {
			continue
		}
		e := &Entry{Severity: p.Severity.String(), State: p.State, Sent: map[string]time.Time{}, ResolvedText: p.resolvedText, Resolved: p.resolved}
		if old := prev.Problems[p.Key]; old != nil && old.State == p.State {
			maps.Copy(e.Sent, old.Sent)
		}
		next.Problems[p.Key] = e
	}
	for ch, ok := range sent {
		for _, it := range items[ch] {
			mark(next, prev, ch, ok, it, now)
		}
	}
	return next
}

// mark records in next what happened to one item sent to ch: an open
// problem that got through is marked as told; a resolution that did not
// is kept for ch alone.
func mark(next, prev State, ch string, ok bool, it Item, now time.Time) {
	switch {
	case ok && it.Kind != Resolved:
		if e := next.Problems[it.Key]; e != nil {
			e.Sent[ch] = now
		}
	case !ok && it.Kind == Resolved:
		e := next.Problems[it.Key]
		if e == nil {
			old := prev.Problems[it.Key]
			e = &Entry{Severity: old.Severity, State: resolvedState, Sent: map[string]time.Time{}, ResolvedText: old.ResolvedText, Resolved: old.Resolved}
			next.Problems[it.Key] = e
		}
		e.Sent[ch] = prev.Problems[it.Key].Sent[ch]
	}
}

// resolvedState marks a problem kept only to tell a channel it cleared.
const resolvedState = "resolved"

// severityOf reads a severity word; unknown words are warnings.
func severityOf(s string) problem.Severity {
	switch s {
	case "info":
		return problem.Info
	case "error":
		return problem.Error
	}
	return problem.Warning
}

// resolvedText says a problem cleared, from its key alone: the state file
// keeps no text.
func resolvedText(key string) string {
	kind, pool, _ := strings.Cut(key, ":")
	f, ok := map[string]string{
		"health": "%s is ONLINE again", "data": "%s reports no data errors", "capacity": "%s is below the capacity warning",
		"fragmentation": "free space in %s is below the fragmentation warning", "scrub": "%s has been scrubbed",
		"checkpoint": "%s holds no old checkpoint",
	}[kind]
	switch {
	case kind == "version":
		return "the userland and kernel module versions match"
	case !ok:
		return key + " cleared"
	}
	return fmt.Sprintf(f, pool)
}

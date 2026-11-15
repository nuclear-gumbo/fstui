// SPDX-License-Identifier: GPL-3.0-or-later

// Package keys is fstui's keymap: every action a key can trigger, with vim-style
// defaults that config.yaml can rebind under keys:. The same key means the same
// thing in every view.
//
// The map is set once at startup, before the UI runs, and only read
// afterwards; views look bindings up when they need them, so a rebinding
// in config.yaml reaches every view without passing a map around.
package keys

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
)

// Action names a thing a key does; the name is the key in config.yaml.
type Action string

// Actions. View-specific wording ("open pool") is given where a view
// shows the binding; Desc is the general meaning shown in help.
const (
	Up       Action = "up"
	Down     Action = "down"
	First    Action = "first"
	Last     Action = "last"
	Left     Action = "left"  // previous tab; collapse a tree node
	Right    Action = "right" // next tab; expand a tree node
	Select   Action = "select"
	Back     Action = "back"
	Quit     Action = "quit"
	Help     Action = "help"
	Refresh  Action = "refresh"
	Bytes    Action = "bytes"
	Hide     Action = "hide_warnings"
	Internal Action = "internal_events"
	Sort     Action = "sort"
	Reverse  Action = "reverse_sort"
	Save     Action = "save"
	Revert   Action = "revert"
	Discard  Action = "discard"
	Confirm  Action = "confirm"
	Write    Action = "write" // Settings: write the reviewed file
	Cancel   Action = "cancel"
	// Write module: changes on the selection, write mode.
	Actions     Action = "actions"
	WriteMode   Action = "write_mode"
	NewSnapshot Action = "new_snapshot"
	// Diff shows what changed since the selected snapshot (read only).
	Diff Action = "diff"
	// Mark and ClearMarks mark items for one change to several.
	Mark       Action = "mark"
	ClearMarks Action = "clear_marks"
	// Events view.
	Filter         Action = "filter"
	FilterPool     Action = "filter_pool"
	FilterSeverity Action = "filter_severity"
	FilterClass    Action = "filter_class"
	SavedFilter    Action = "saved_filter"
	Follow         Action = "follow"
)

type def struct {
	keys []string
	desc string
}

// defaults are vim-style; arrows and the usual alternatives work too.
var defaults = map[Action]def{
	Up:       {[]string{"k", "up"}, "move up"},
	Down:     {[]string{"j", "down"}, "move down"},
	First:    {[]string{"g", "home"}, "first"},
	Last:     {[]string{"G", "end"}, "last"},
	Left:     {[]string{"h", "left"}, "previous tab, collapse"},
	Right:    {[]string{"l", "right"}, "next tab, expand"},
	Select:   {[]string{"enter", "space"}, "open"},
	Back:     {[]string{"esc", "q"}, "back, quit at top level"},
	Quit:     {[]string{"ctrl+c"}, "quit"},
	Help:     {[]string{"?"}, "help"},
	Refresh:  {[]string{"r"}, "refresh"},
	Bytes:    {[]string{"b"}, "exact bytes / IEC units"},
	Hide:     {[]string{"x"}, "hide warnings"},
	Internal: {[]string{"i"}, "internal events"},
	Sort:     {[]string{"s"}, "sort column"},
	Reverse:  {[]string{"S"}, "reverse sort"},
	Save:     {[]string{"p"}, "review changes"},
	Write:    {[]string{"w"}, "write the file"},
	Revert:   {[]string{"u"}, "revert to the previous file"},
	Discard:  {[]string{"d"}, "discard unsaved changes"},
	Confirm:  {[]string{"y"}, "confirm"},
	Cancel:   {[]string{"n"}, "cancel"},
	// W, not w: w writes the reviewed file in Settings.
	Actions:     {[]string{"a"}, "changes for the selection"},
	WriteMode:   {[]string{"W"}, "write mode on/off"},
	NewSnapshot: {[]string{"n"}, "new snapshot"},
	Diff:        {[]string{"="}, "what changed since this snapshot"},
	Mark:        {[]string{"m"}, "mark for a change to several"},
	ClearMarks:  {[]string{"M"}, "clear the marks"},

	// p is Settings' save, and a key means the same everywhere, so the
	// pool filter is on P.
	Filter:         {[]string{"/"}, "filter by text"},
	FilterPool:     {[]string{"P"}, "next pool"},
	FilterSeverity: {[]string{"v"}, "next minimum severity"},
	FilterClass:    {[]string{"c"}, "next class"},
	SavedFilter:    {[]string{"f"}, "next saved filter"},
	Follow:         {[]string{"F"}, "follow the newest"},
}

// current is the map in use; Configure replaces it once at startup.
var current = build(nil)

func build(over map[Action][]string) map[Action]key.Binding {
	m := make(map[Action]key.Binding, len(defaults))
	for a, d := range defaults {
		ks := d.keys
		if o, ok := over[a]; ok {
			ks = o
		}
		m[a] = key.NewBinding(key.WithKeys(ks...), key.WithHelp(strings.Join(ks, "/"), d.desc))
	}
	return m
}

// Get returns the binding for an action, with its general help text.
func Get(a Action) key.Binding { return current[a] }

// As returns the binding for an action with view-specific help text, such
// as "open pool" for Select in the Pools view.
func As(a Action, desc string) key.Binding {
	b := current[a]
	b.SetHelp(b.Help().Key, desc)
	return b
}

// Configure applies keys: from config.yaml, action name to keys. It
// returns a problem for each unknown action, and leaves the defaults in
// place for those. Call it before the UI starts.
func Configure(over map[string][]string) []string {
	var problems []string
	valid := make(map[Action][]string, len(over))
	names := make([]string, 0, len(over))
	for n := range over {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := Action(n)
		if _, ok := defaults[a]; !ok {
			problems = append(problems, fmt.Sprintf("keys: unknown action %q (actions: %s)", n, strings.Join(Names(), ", ")))
			continue
		}
		if len(over[n]) == 0 {
			problems = append(problems, fmt.Sprintf("keys.%s: no keys given; keeping %s", n, strings.Join(defaults[a].keys, ", ")))
			continue
		}
		valid[a] = over[n]
	}
	current = build(valid)
	return append(problems, duplicates(valid)...)
}

// duplicates reports a key the user bound to two actions that are
// active in the same views. Left and Right are shared on purpose by tabs
// and trees and are never both in effect, so only explicit rebindings are
// checked against each other and against the defaults.
func duplicates(changed map[Action][]string) []string {
	var out []string
	for a, ks := range changed {
		for b, binding := range current {
			if a == b {
				continue
			}
			for _, k := range ks {
				if slices.Contains(binding.Keys(), k) {
					out = append(out, fmt.Sprintf("keys.%s: %q is also bound to %s", a, k, b))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// Names lists the action names, for messages and the help overlay.
func Names() []string {
	out := make([]string, 0, len(defaults))
	for a := range defaults {
		out = append(out, string(a))
	}
	sort.Strings(out)
	return out
}

// Reset restores the defaults; tests use it.
func Reset() { current = build(nil) }

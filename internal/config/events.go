// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Events is the events: section.
type Events struct {
	Retention int // events kept in memory
	Filters   []EventFilter
}

// EventFilter is a saved filter for the Events view. Empty fields match
// everything.
type EventFilter struct {
	Name       string
	Severities []string // "info", "notice", "warn", "error"
	Pool       string   // exact pool name
	Class      string   // part of the event class, e.g. "checksum"
	Text       string   // part of any field
}

const (
	defaultRetention = 5000
	retentionMin     = 100
	retentionMax     = 100000
	filterNameMax    = 40
)

// SeverityNames are the severities a filter may name, least urgent first.
var SeverityNames = []string{"info", "notice", "warn", "error"}

var eventKeys = map[string]setter{
	"retention": positiveIn(func(c *Config) *int { return &c.Events.Retention }, retentionMin, retentionMax),
	"filters":   setFilters,
}

// positiveIn sets a whole number from lo to hi.
func positiveIn(field func(*Config) *int, lo, hi int) setter {
	return func(_ *applier, c *Config, n *yaml.Node) error {
		v, err := intIn(n, lo, hi)
		if err != nil {
			return err
		}
		*field(c) = v
		return nil
	}
}

// setFilters reads a list of saved filters. A filter with a problem is
// reported and left out; the others apply. A file that sets filters
// replaces the filters of the files before it.
func setFilters(a *applier, c *Config, n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("want a list of filters, each with a name")
	}
	var out []EventFilter
	for i, item := range n.Content {
		f, err := filterOf(item)
		if err != nil {
			a.report(item, "events.filters[%d]: %v", i, err)
			continue
		}
		if slices.ContainsFunc(out, func(g EventFilter) bool { return g.Name == f.Name }) {
			a.report(item, "events.filters[%d]: a filter named %q is already listed", i, f.Name)
			continue
		}
		out = append(out, f)
	}
	c.Events.Filters = out
	return nil
}

func filterOf(n *yaml.Node) (EventFilter, error) {
	var f EventFilter
	if n.Kind != yaml.MappingNode {
		return f, fmt.Errorf("want a mapping with name and severity, pool, class or text")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		var err error
		switch k.Value {
		case "name":
			f.Name, err = scalar(v, "!!str", "a name")
			if err == nil && (f.Name == "" || len(f.Name) > filterNameMax) {
				err = fmt.Errorf("want a name of 1 to %d characters", filterNameMax)
			}
		case "severity":
			f.Severities, err = severities(v)
		case "pool":
			f.Pool, err = scalar(v, "!!str", "a pool name")
		case "class":
			f.Class, err = scalar(v, "!!str", "part of an event class, such as checksum")
		case "text":
			f.Text, err = scalar(v, "!!str", "text to look for")
		default:
			err = fmt.Errorf("unknown setting %q (name, severity, pool, class, text)", k.Value)
		}
		if err != nil {
			return f, fmt.Errorf("%s: %w", k.Value, err)
		}
	}
	if f.Name == "" {
		return f, fmt.Errorf("a filter needs a name")
	}
	return f, nil
}

// severities reads one severity or a list of them.
func severities(n *yaml.Node) ([]string, error) {
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	var out []string
	for _, item := range items {
		s, err := oneOf(item, SeverityNames...)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

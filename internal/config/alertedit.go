// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"slices"

	"go.yaml.in/yaml/v3"
)

// EditAlerts replaces notifications.alerts and notifications.templates
// in the YAML of a config file and returns the new document; the rest of
// the file, its comments and key order stay. No templates removes the
// key; no alerts removes it too, which brings back the default alerts.
func EditAlerts(current []byte, alerts []Alert, templates []Template) ([]byte, error) {
	doc, root, err := document(current)
	if err != nil {
		return nil, err
	}
	notif := section(root, "notifications")
	list := func(key string, items []*yaml.Node) {
		if len(items) == 0 {
			removeKey(notif, key)
			return
		}
		setValue(notif, key, &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items})
	}
	var ts, as []*yaml.Node
	for _, t := range templates {
		ts = append(ts, templateNode(t))
	}
	for _, a := range alerts {
		as = append(as, alertNode(a))
	}
	list("templates", ts)
	list("alerts", as)
	return encode(doc)
}

// removeKey deletes key from the mapping m.
func removeKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = slices.Delete(m.Content, i, i+2)
			return
		}
	}
}

// mapping builds a mapping node from key and value pairs, leaving out
// empty values.
func mapping(pairs ...string) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: pairs[i]}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pairs[i+1]})
		}
	}
	return m
}

func templateNode(t Template) *yaml.Node {
	return mapping("name", t.Name, "description", t.Desc, "metric", t.Metric, "op", t.Op, "value", t.Value,
		"error_value", t.ErrorValue, "severity", t.Severity, "text", t.Text, "resolved_text", t.ResolvedText)
}

func alertNode(a Alert) *yaml.Node {
	m := mapping("template", a.Template)
	names := func(key string, l []string) {
		if len(l) == 0 {
			return
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, s := range l {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s})
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, seq)
	}
	names("pools", a.Pools)
	names("channels", a.Channels)
	rest := mapping("value", a.Value, "error_value", a.ErrorValue, "severity", a.Severity, "text", a.Text, "resolved_text", a.ResolvedText)
	m.Content = append(m.Content, rest.Content...)
	if a.Repeat != nil {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "repeat"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmtDuration(*a.Repeat)})
	}
	if a.Resolved != nil {
		v := "false"
		if *a.Resolved {
			v = "true"
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "resolved"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v})
	}
	return m
}

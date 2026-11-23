// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "go.yaml.in/yaml/v3"

// EditHosts replaces hosts: in the YAML of a config file with hosts, and
// notifications.hosts with checked unless it is nil, and returns the new
// document; the rest of the file, its comments and key order stay.
func EditHosts(current []byte, hosts []Host, checked []string) ([]byte, error) {
	doc, root, err := document(current)
	if err != nil {
		return nil, err
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, h := range hosts {
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
		for _, kv := range [][2]string{{"name", h.Name}, {"destination", h.Destination}} {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: kv[0]}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv[1]})
		}
		seq.Content = append(seq.Content, m)
	}
	setValue(root, "hosts", seq)
	if checked != nil {
		names := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, n := range checked {
			names.Content = append(names.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: n})
		}
		setValue(section(root, "notifications"), "hosts", names)
	}
	return encode(doc)
}

// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// EditChannels replaces notifications.channels in the YAML of a config
// file with chs and returns the new document; the rest of the file, its
// comments and key order stay.
func EditChannels(current []byte, chs []Channel) ([]byte, error) {
	doc, root, err := document(current)
	if err != nil {
		return nil, err
	}
	notif := section(root, "notifications")
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, ch := range chs {
		seq.Content = append(seq.Content, channelNode(ch))
	}
	setValue(notif, "channels", seq)
	return encode(doc)
}

// EditSecrets sets and removes secrets in the YAML of a secrets file: set
// maps a channel name to its new secret, remove lists names to drop.
func EditSecrets(current []byte, set map[string]string, remove []string) ([]byte, error) {
	doc, root, err := document(current)
	if err != nil {
		return nil, errors.New("the current secrets file is not YAML") // its parser's message could quote a secret
	}
	for _, name := range remove {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == name {
				root.Content = append(root.Content[:i], root.Content[i+2:]...)
				break
			}
		}
	}
	for name, v := range set {
		setValue(root, name, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle})
	}
	return encode(doc)
}

// document parses a file into its root mapping, creating one for an
// empty file.
func document(current []byte) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(current, &doc); err != nil {
		return nil, nil, fmt.Errorf("reading the current file: %w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, errors.New("the current file is not a mapping of settings")
	}
	return &doc, root, nil
}

// section is the mapping under key in m, added at the end if missing.
func section(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key && m.Content[i+1].Kind == yaml.MappingNode {
			return m.Content[i+1]
		}
	}
	s := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setValue(m, key, s)
	return s
}

// setValue sets key in m to v, keeping its place if it is there.
func setValue(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}

func encode(doc *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("writing the new file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("writing the new file: %w", err)
	}
	return b.Bytes(), nil
}

// channelNode writes a channel as config.yaml spells it, with only the
// settings it has.
func channelNode(ch Channel) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	str := func(k, v string) {
		if v != "" {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
		}
	}
	str("name", ch.Name)
	str("type", ch.Type)
	str("url", ch.URL)
	str("topic", ch.Topic)
	str("chat_id", ch.ChatID)
	str("room", ch.Room)
	str("user_key", ch.UserKey)
	str("host", ch.Host)
	if ch.Port != 0 {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "port"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(ch.Port)})
	}
	str("from", ch.From)
	switch len(ch.To) {
	case 0:
	case 1:
		str("to", ch.To[0])
	default:
		to := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, a := range ch.To {
			to.Content = append(to.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: a})
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "to"}, to)
	}
	str("user", ch.User)
	str("ca_file", ch.CAFile)
	str("secret_file", ch.SecretFile)
	if ch.InsecureHTTP {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "insecure_http"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
	if ch.MinSeverity != defaultMinSeverity {
		str("min_severity", ch.MinSeverity)
	}
	return m
}

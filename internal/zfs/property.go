// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unique"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// SourceKind says where a property's value comes from (zfsprops(7)).
type SourceKind uint8

// Property sources. SourceOther covers a source word fstui does not know;
// Source.Word returns the word itself.
const (
	SourceNone SourceKind = iota + 1
	SourceDefault
	SourceLocal
	SourceInherited
	SourceTemporary
	SourceReceived
	SourceOther
)

// Source is the origin of a property value. It is small on purpose: every
// property of every dataset and snapshot carries one.
type Source struct {
	text string // the parent for SourceInherited, the word for SourceOther
	Kind SourceKind
}

// Word is the source as OpenZFS printed it, e.g. "INHERITED".
func (s Source) Word() string {
	if s.Kind == SourceOther || int(s.Kind) >= len(sourceNames) {
		return s.text
	}
	return sourceNames[s.Kind]
}

// From is the dataset a SourceInherited value comes from.
func (s Source) From() termtext.Text {
	if s.Kind == SourceInherited {
		return termtext.New(s.text)
	}
	return termtext.Text{}
}

// Property is one property value. Numbers, given as JSON numbers by
// --json-int or as digit strings by -p, set IsNumber; "not applicable" is
// the string "-".
type Property struct {
	Text     termtext.Text // the value as text, decimal for numbers
	Number   uint64
	IsNumber bool
	Source   Source
}

// Properties maps property names to values.
type Properties map[string]Property

type jsonProperty struct {
	Value  json.RawMessage `json:"value"`
	Source struct {
		Type string `json:"type"`
		Data string `json:"data"`
	} `json:"source"`
}

func (p jsonProperty) parse(name string) (Property, error) {
	out := Property{Source: parseSource(p.Source.Type, p.Source.Data)}
	if len(p.Value) > 0 && p.Value[0] == '"' {
		var s string
		if err := json.Unmarshal(p.Value, &s); err != nil {
			return Property{}, fmt.Errorf("property %s: %w", name, err)
		}
		// With -p instead of --json-int, numbers come as digit strings
		// (exact, see fstui's use of -p for property reads).
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			out.Number, out.IsNumber = n, true
		} else {
			s = internShort(s) // on, off, -, lz4: the same few words on every dataset
		}
		out.Text = termtext.New(s)
		return out, nil
	}
	n, err := strconv.ParseUint(string(p.Value), 10, 64)
	if err != nil {
		return Property{}, fmt.Errorf("property %s: value %s is neither a string nor an unsigned integer: %w", name, p.Value, err)
	}
	out.Number, out.IsNumber = n, true
	out.Text = termtext.New(string(p.Value))
	return out, nil
}

// sourceNames are the known words by kind, for Source.Word.
var sourceNames = [...]string{
	SourceNone: "NONE", SourceDefault: "DEFAULT", SourceLocal: "LOCAL", SourceInherited: "INHERITED",
	SourceTemporary: "TEMPORARY", SourceReceived: "RECEIVED",
}

// sourceWords are the source words OpenZFS prints; a known word is kept
// as this constant instead of a copy per property.
var sourceWords = map[string]SourceKind{
	"NONE": SourceNone, "DEFAULT": SourceDefault, "LOCAL": SourceLocal, "INHERITED": SourceInherited,
	"TEMPORARY": SourceTemporary, "RECEIVED": SourceReceived,
}

func parseSource(word, data string) Source {
	kind, ok := sourceWords[word]
	switch {
	case !ok:
		return Source{Kind: SourceOther, text: intern(word)}
	case kind == SourceInherited:
		return Source{Kind: kind, text: intern(data)} // a few parents, inherited by many
	}
	return Source{Kind: kind}
}

func parseProperties(raw map[string]jsonProperty) (Properties, error) {
	props := make(Properties, len(raw))
	for name, jp := range raw {
		p, err := jp.parse(name)
		if err != nil {
			return nil, err
		}
		props[intern(name)] = p
	}
	return props, nil
}

// intern returns one shared copy of s. Property names, source words and
// short values repeat on every dataset and snapshot; with tens of
// thousands of snapshots, a copy each costs megabytes.
func intern(s string) string { return unique.Make(s).Value() }

// internShort interns s if it is short enough to be one of a few words
// that repeat; longer values (mountpoints, descriptions) are mostly
// different from one another.
func internShort(s string) string {
	if len(s) > 24 {
		return s
	}
	return intern(s)
}

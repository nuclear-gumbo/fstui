// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// SourceKind says where a property's value comes from (zfsprops(7)).
type SourceKind int

// Property sources. SourceOther covers a source word fstui does not know;
// the word itself is kept in Source.Word.
const (
	SourceNone SourceKind = iota + 1
	SourceDefault
	SourceLocal
	SourceInherited
	SourceTemporary
	SourceReceived
	SourceOther
)

// Source is the origin of a property value.
type Source struct {
	Kind SourceKind
	Word string        // as OpenZFS printed it, e.g. "INHERITED"
	From termtext.Text // for SourceInherited: the dataset it is inherited from
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
		out.Text = termtext.New(s)
		// With -p instead of --json-int, numbers come as digit strings
		// (exact, see fstui's use of -p for property reads).
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			out.Number, out.IsNumber = n, true
		}
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

func parseSource(word, data string) Source {
	s := Source{Word: word}
	switch word {
	case "NONE":
		s.Kind = SourceNone
	case "DEFAULT":
		s.Kind = SourceDefault
	case "LOCAL":
		s.Kind = SourceLocal
	case "INHERITED":
		s.Kind = SourceInherited
		s.From = termtext.New(data)
	case "TEMPORARY":
		s.Kind = SourceTemporary
	case "RECEIVED":
		s.Kind = SourceReceived
	default:
		s.Kind = SourceOther
	}
	return s
}

func parseProperties(raw map[string]jsonProperty) (Properties, error) {
	props := make(Properties, len(raw))
	for name, jp := range raw {
		p, err := jp.parse(name)
		if err != nil {
			return nil, err
		}
		props[name] = p
	}
	return props, nil
}

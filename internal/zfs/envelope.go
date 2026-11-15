// SPDX-License-Identifier: GPL-3.0-or-later

// Package zfs is fstui's OpenZFS backend: it parses the JSON output of the
// zfs and zpool commands (OpenZFS 2.3 and newer) into typed values.
package zfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// supportedOutputMajor is the output_version.vers_major fstui understands.
// Every OpenZFS JSON document carries it; a different major version means
// the layout changed and fields would be misread.
const supportedOutputMajor = 0

// ErrOutputVersion is returned for JSON output of an unknown major version.
var ErrOutputVersion = errors.New("unsupported OpenZFS JSON output version")

type outputVersion struct {
	OutputVersion struct {
		Command   string `json:"command"`
		VersMajor int    `json:"vers_major"`
		VersMinor int    `json:"vers_minor"`
	} `json:"output_version"`
}

// decode checks the output version of a JSON document from command and
// unmarshals it into v. Numbers decode into the integer types of v, so
// 64-bit GUIDs are not rounded through float64.
func decode(data []byte, command string, v any) error {
	var ov outputVersion
	if err := json.Unmarshal(data, &ov); err != nil {
		return fmt.Errorf("parsing %s output: %w", command, err)
	}
	got := ov.OutputVersion
	if got.Command != command {
		return fmt.Errorf("parsing %s output: document is from %q", command, got.Command)
	}
	if got.VersMajor != supportedOutputMajor {
		return fmt.Errorf("parsing %s output: version %d.%d: %w", command, got.VersMajor, got.VersMinor, ErrOutputVersion)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing %s output: %w", command, err)
	}
	return nil
}

// jsonUint is an unsigned integer that OpenZFS prints as a JSON number
// with --json-int and as a digit string with -p.
type jsonUint uint64

func (u *jsonUint) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(b, `"`))
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("%s is not an unsigned integer: %w", b, err)
	}
	*u = jsonUint(n)
	return nil
}

// member is one key and value of a JSON object, in document order.
type member struct {
	Key   string
	Value json.RawMessage
}

// orderedObject returns the members of a JSON object in the order they
// appear. OpenZFS emits vdev trees as objects whose order is meaningful
// (mirror-0 before mirror-1), which a Go map would lose.
func orderedObject(raw json.RawMessage) ([]member, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("reading object: %w", err)
	} else if tok != json.Delim('{') {
		return nil, fmt.Errorf("expected an object, found %v", tok)
	}
	var out []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading object key: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("object key is %T, not a string", tok)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("reading value of %q: %w", key, err)
		}
		out = append(out, member{Key: key, Value: v})
	}
	return out, nil
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// CompatibilityDirs are where zpool looks for the files a pool's
// compatibility property names (zpool-features(7)), in order; the last
// is NixOS's system profile, where the files are not under /usr.
var CompatibilityDirs = []string{"/etc/zfs/compatibility.d", "/usr/share/zfs/compatibility.d", "/run/current-system/sw/share/zfs/compatibility.d"}

// ParseCompatibility reads a compatibility file: feature names separated
// by white space or commas, with # comments.
func ParseCompatibility(b []byte) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		out = append(out, strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' })...)
	}
	return out
}

// CompatibleFeatures reads the features a compatibility value allows:
// the features every named file lists. ok is false for "off", which
// allows every feature; "legacy" allows none.
func (r *Reader) CompatibleFeatures(value string) (allowed map[string]bool, ok bool, err error) {
	switch value {
	case "", "off":
		return nil, false, nil
	case "legacy":
		return map[string]bool{}, true, nil
	}
	for i, name := range strings.Split(value, ",") {
		if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return nil, false, fmt.Errorf("compatibility %s: %s is not a file name", termtext.New(value), termtext.New(name))
		}
		feats, err := r.compatFile(name)
		if err != nil {
			return nil, false, err
		}
		set := map[string]bool{}
		for _, f := range feats {
			if i == 0 || allowed[f] {
				set[f] = true
			}
		}
		allowed = set
	}
	return allowed, true, nil
}

func (r *Reader) compatFile(name string) ([]string, error) {
	for _, dir := range CompatibilityDirs {
		b, err := r.file(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("compatibility file %s: %w", termtext.New(name), err)
		}
		return ParseCompatibility(b), nil
	}
	return nil, fmt.Errorf("compatibility file %s is in none of %s", termtext.New(name), strings.Join(CompatibilityDirs, ", "))
}

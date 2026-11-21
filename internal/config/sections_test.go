// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "testing"

// Every setting has a label and is in exactly one section, so a setting
// added later shows in the Config view where a person would look for it.
func TestEverySettingHasLabelAndSection(t *testing.T) {
	for _, s := range Settings(nil) {
		if _, ok := labels[s.Path]; !ok {
			t.Errorf("%s has no label in sections.go", s.Path)
		}
		n := 0
		for _, sec := range Sections {
			for _, p := range sec.Paths {
				if s.Path == p || p[len(p)-1] == '.' && len(s.Path) > len(p) && s.Path[:len(p)] == p {
					n++
				}
			}
		}
		if n != 1 {
			t.Errorf("%s is in %d sections", s.Path, n)
		}
	}
	for path := range labels {
		if _, ok := byPath(path); !ok {
			t.Errorf("label for %s, which is not a setting", path)
		}
	}
}

func byPath(path string) (Spec, bool) {
	for _, s := range Settings(nil) {
		if s.Path == path {
			return s, true
		}
	}
	return Spec{}, false
}

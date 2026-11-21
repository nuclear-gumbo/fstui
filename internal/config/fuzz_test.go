// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "testing"

// No file, however malformed, makes Parse panic, and whatever it returns
// passes the checks every loaded configuration passes.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"theme: default\n", "thresholds: {capacity_warn: 80}\n", "keys: {select: [enter]}\n",
		"colors: {warn: {dark: \"#000000\"}}\n", "notifications: {channels: [{name: a, type: ntfy, url: \"https://x\"}]}\n",
		"events: {filters: [{name: x, severity: [error]}]}\n", "a: &x 1\nb: *x\n", "[1, 2]\n", "",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		c, _ := Parse("fuzz.yaml", data)
		if problems := c.check(); len(problems) > 0 {
			t.Errorf("Parse returned a configuration its own checks refuse: %v", problems)
		}
	})
}

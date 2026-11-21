// SPDX-License-Identifier: GPL-3.0-or-later

package schedule

import "testing"

// parseCron reads any file without panicking, and what cronFile writes
// from its result reads back the same.
func FuzzParseCron(f *testing.F) {
	f.Add([]byte(cronHeader + "# fstui: scrub:tank weekly\n0 3 * * 0 root /usr/sbin/zpool scrub tank\n"))
	f.Add([]byte("# fstui: \n# fstui: x\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		first := parseCron(data)
		again := parseCron([]byte(cronFile(first)))
		for id, e := range first {
			if !validEntry(id, e) {
				continue // not one fstui writes
			}
			if again[id] != e {
				t.Errorf("entry %q: %+v read back as %+v", id, e, again[id])
			}
		}
	})
}

// validEntry is an entry fstui could have written: one line each.
func validEntry(id string, e cronEntry) bool {
	for _, s := range []string{id, e.when, e.line} {
		for _, r := range s {
			if r == '\n' || r == '\r' {
				return false
			}
		}
	}
	return id != "" && !hasSpace(id)
}

func hasSpace(s string) bool {
	for _, r := range s {
		if r == ' ' {
			return true
		}
	}
	return false
}

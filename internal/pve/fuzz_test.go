// SPDX-License-Identifier: GPL-3.0-or-later

package pve

import (
	"os"
	"path/filepath"
	"testing"
)

func seed(f *testing.F, file string) {
	f.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "pve", "9.2", file))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
}

// Whatever a cluster's files hold, the parsers return or refuse; a
// storage they return names a valid pool.
func FuzzParseMembers(f *testing.F) {
	seed(f, "members.json")
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := ParseMembers(b)
		if err == nil && (m.Self == "" || len(m.Nodes) == 0) {
			t.Errorf("accepted without a node: %+v", m)
		}
	})
}

func FuzzParseStorage(f *testing.F) {
	seed(f, "storage.cfg")
	f.Fuzz(func(t *testing.T, b []byte) {
		ss, err := ParseStorage(b)
		for _, s := range ss {
			if err == nil && s.Pool.String() == "" {
				t.Errorf("a storage without a pool: %+v", s)
			}
		}
	})
}

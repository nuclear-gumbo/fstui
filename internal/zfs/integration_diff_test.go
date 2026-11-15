// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

func TestIntegrationDiff(t *testing.T) {
	p := testpool.New(t, "", 1)
	mnt := filepath.Join(testpool.Dir, "mnt", p.Name)
	for name, body := range map[string]string{"keep": "a", "gone": "b"} {
		if err := os.WriteFile(filepath.Join(mnt, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snap := p.Snapshot("", "s1")
	if err := os.WriteFile(filepath.Join(mnt, "keep"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(mnt, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mnt, "new \x1b[2J"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, truncated, err := NewReader(runner(t)).Diff(t.Context(), snap, "")
	if err != nil || truncated {
		t.Fatal(err)
	}
	seen := map[string]byte{}
	for _, e := range got {
		seen[filepath.Base(e.Path.Raw())] = e.Change
	}
	if seen["keep"] != 'M' || seen["gone"] != '-' || seen["new \x1b[2J"] != '+' {
		t.Fatalf("diff = %v", seen)
	}
}

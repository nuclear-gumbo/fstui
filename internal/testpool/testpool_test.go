// SPDX-License-Identifier: GPL-3.0-or-later

package testpool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

func TestGuardAcceptsTestPools(t *testing.T) {
	for _, name := range []string{
		"fstuitest_x",
		"fstuitest_ia1b2c3d4",
		"fstuitest_x/home",
		"fstuitest_x/home/user@daily-1",
		"fstuitest_x@snap",
		"fstuitest_x#bookmark",
	} {
		if err := Guard(name); err != nil {
			t.Errorf("Guard(%q) = %v", name, err)
		}
	}
}

func TestGuardRefusesEverythingElse(t *testing.T) {
	for _, name := range []string{
		"",
		"tank",
		"fstuitest_",   // the prefix alone
		"fstuitest_/a", // empty suffix before the first component
		"fstuitest",
		"fstuitestx",
		"Fstuitest_x",
		"-fstuitest_x",
		"xfstuitest_x",
		"tank/fstuitest_x",   // prefix in a child, not the pool
		"tank@fstuitest_x",   // prefix in a snapshot name
		"tank#fstuitest_x",   // prefix in a bookmark name
		" fstuitest_x",       // leading space
		"tank/a/fstuitest_x", // deeper child
	} {
		if err := Guard(name); err == nil {
			t.Errorf("Guard(%q) accepted a name outside the test pools", name)
		}
	}
}

// A command aimed at a non-test pool must be refused before a process is
// started. The Pool here has no resolved tools, so reaching the exec step
// would panic instead of returning the guard's error.
func TestExecRefusesBeforeRunning(t *testing.T) {
	p := &Pool{Name: "tank", t: t}
	err := p.exec(execute.Zpool, "tank", []string{"destroy", "-f", "--", "tank"})
	if err == nil || !strings.Contains(err.Error(), "not a test pool") {
		t.Fatalf("err = %v, want the guard's refusal", err)
	}
}

// The pool directory is in /var/tmp, which anyone can write. Root must
// refuse one it does not own outright: a symlink, someone else's
// directory, or one others can write.
func TestPrepareDirRefusesForeignDirectories(t *testing.T) {
	uid := uint32(os.Geteuid()) //nolint:gosec // uids are non-negative
	base := t.TempDir()

	fresh := filepath.Join(base, "fresh")
	if err := prepareDir(fresh, uid); err != nil {
		t.Fatalf("a new directory: %v", err)
	}
	if err := prepareDir(fresh, uid); err != nil {
		t.Fatalf("the same directory again: %v", err)
	}
	if err := prepareDir(fresh, uid+1); err == nil {
		t.Error("a directory owned by another uid was accepted")
	}

	open := filepath.Join(base, "open")
	if err := os.Mkdir(open, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // the test needs a world-writable directory
		t.Fatal(err)
	}
	if err := prepareDir(open, uid); err == nil {
		t.Error("a world-writable directory was accepted")
	}

	link := filepath.Join(base, "link")
	if err := os.Symlink(fresh, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareDir(link, uid); err == nil {
		t.Error("a symlink to a directory was accepted")
	}
}

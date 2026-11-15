// SPDX-License-Identifier: GPL-3.0-or-later

package testpool

import (
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

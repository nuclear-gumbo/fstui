// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"syscall"
	"testing"
)

func TestHardenTightensUmaskAndDumpable(t *testing.T) {
	old := syscall.Umask(0o002)
	defer syscall.Umask(old)
	if err := harden(); err != nil {
		t.Fatal(err)
	}
	if got := syscall.Umask(0o022); got != 0o022 {
		t.Errorf("umask after harden = %#o, want %#o", got, 0o022)
	}
	syscall.Umask(0o077)
	if err := harden(); err != nil {
		t.Fatal(err)
	}
	if got := syscall.Umask(0o077); got != 0o077 {
		t.Errorf("a stricter umask became %#o", got)
	}
	if d, err := dumpable(); err != nil || d {
		t.Errorf("dumpable = %v, %v; want false", d, err)
	}
}

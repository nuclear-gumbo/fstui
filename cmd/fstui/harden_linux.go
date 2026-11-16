// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"syscall"
)

// minUmask is masked out of every file fstui and the commands it runs
// create: tee writes /etc/modprobe.d as root, and a file there that others
// can write runs their commands as root when the module loads.
const minUmask = 0o022

// prSetDumpable and prGetDumpable are prctl(2) options.
const (
	prSetDumpable = 4
	prGetDumpable = 3
)

// harden runs before anything else. It keeps the umask at least 022, and
// marks the process not dumpable: no core dump can hold a passphrase,
// and processes of the same user cannot read fstui's memory with ptrace
// (prctl(2), PR_SET_DUMPABLE).
func harden() error {
	syscall.Umask(syscall.Umask(0) | minUmask)
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); errno != 0 {
		return fmt.Errorf("prctl(PR_SET_DUMPABLE): %w", errno)
	}
	return nil
}

// dumpable reports the process's dumpable flag.
func dumpable() (bool, error) {
	r, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prGetDumpable, 0, 0)
	if errno != 0 {
		return false, fmt.Errorf("prctl(PR_GET_DUMPABLE): %w", errno)
	}
	return r != 0, nil
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// Lock is the single-instance lock of write mode: flock(2) on a file holding the PID of the holder.
type Lock struct{ f *os.File }

// TakeLock takes the lock at path without waiting. If another process
// holds it, the error names that process's PID.
func TakeLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // a fixed path chosen by fstui
	if err != nil {
		return nil, fmt.Errorf("instance lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := make([]byte, 32)
		n, _ := f.ReadAt(holder, 0)
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			pid := string(bytes.TrimSpace(holder[:n]))
			if _, perr := strconv.Atoi(pid); perr != nil {
				pid = "unknown"
			}
			return nil, fmt.Errorf("another fstui (PID %s) is in write mode; %s is locked", pid, path)
		}
		return nil, fmt.Errorf("instance lock %s: %w", path, err)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &Lock{f: f}, nil
}

// Release gives the lock up.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = l.f.Truncate(0)
	_ = l.f.Close() // closing releases the flock
	l.f = nil
}

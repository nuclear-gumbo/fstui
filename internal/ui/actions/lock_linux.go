// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"errors"
	"fmt"

	"github.com/nuclear-gumbo/fstui/internal/statedir"
)

// Lock is the single-instance lock of write mode.
type Lock = statedir.Lock

// TakeLock takes write mode's lock at path without waiting. If another
// process holds it, the error names that process's PID.
func TakeLock(path string) (*Lock, error) {
	l, err := statedir.TakeLock(path)
	if errors.Is(err, statedir.ErrLocked) {
		return nil, fmt.Errorf("another fstui is in write mode: %w", err)
	}
	return l, err //nolint:wrapcheck // statedir names the lock
}

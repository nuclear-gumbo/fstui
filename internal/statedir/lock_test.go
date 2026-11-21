// SPDX-License-Identifier: GPL-3.0-or-later

package statedir

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A second taker is refused with the holder's PID until the first
// releases the lock.
func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.lock")
	l, err := TakeLock(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = TakeLock(path)
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Fatalf("second lock: %v", err)
	}
	l.Release()
	l2, err := TakeLock(path)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	l2.Release()
	l2.Release() // twice is harmless
}

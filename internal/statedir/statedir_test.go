// SPDX-License-Identifier: GPL-3.0-or-later

package statedir

import (
	"errors"
	"os"
	"testing"
)

func TestRuntimeDir(t *testing.T) {
	if os.Geteuid() == 0 {
		if d, err := RuntimeDir(); err != nil || d != rootRuntime {
			t.Errorf("root: %q %v", d, err)
		}
		return
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if d, err := RuntimeDir(); err != nil || d != "/run/user/1000/fstui" {
		t.Errorf("%q %v", d, err)
	}
	for _, v := range []string{"", "run/user/1000"} {
		t.Setenv("XDG_RUNTIME_DIR", v)
		if _, err := RuntimeDir(); !errors.Is(err, ErrNoRuntimeDir) {
			t.Errorf("XDG_RUNTIME_DIR=%q: %v", v, err)
		}
	}
}

// A directory someone else could open is refused, not used.
func TestPrivate(t *testing.T) {
	d := t.TempDir() + "/fstui"
	if err := Private(d); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Private(d); err == nil {
		t.Error("a directory others can open was accepted")
	}
}

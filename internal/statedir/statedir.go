// SPDX-License-Identifier: GPL-3.0-or-later

// Package statedir is where fstui keeps what it must remember between
// runs (saved plans, what was notified): /var/lib/fstui for root,
// $XDG_STATE_HOME/fstui for others, in directories no one else can open.
package statedir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// rootDir is root's state directory.
const rootDir = "/var/lib/fstui"

// Dir is the effective user's state directory; it is not created.
func Dir() (string, error) {
	if os.Geteuid() == 0 {
		return rootDir, nil
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no state directory: %w", err)
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "fstui"), nil
}

// rootRuntime is root's runtime directory.
const rootRuntime = "/run/fstui"

// ErrNoRuntimeDir is returned by RuntimeDir for a user without
// XDG_RUNTIME_DIR, which systemd-logind and elogind set at login.
var ErrNoRuntimeDir = errors.New("XDG_RUNTIME_DIR is not set to an absolute path; log in through a session that sets it")

// RuntimeDir is where fstui keeps what lasts only while it runs, such as
// ssh's connection sockets: /run/fstui for root, $XDG_RUNTIME_DIR/fstui
// for others. It is not created.
func RuntimeDir() (string, error) {
	if os.Geteuid() == 0 {
		return rootRuntime, nil
	}
	run := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(run) {
		return "", ErrNoRuntimeDir
	}
	return filepath.Join(run, "fstui"), nil
}

// Private creates dir (mode 0700) if it is missing and refuses it unless
// it is a real directory owned by the effective user with no access for
// anyone else. A directory another user created first, for example under
// /tmp, is refused, not used.
func Private(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err //nolint:wrapcheck // callers say what the directory is for
	}
	return Check(dir)
}

// Check is Private without creating the directory.
func Check(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err //nolint:wrapcheck // as above
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Geteuid() || fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be a directory owned by uid %d with no access for others", dir, os.Geteuid())
	}
	return nil
}

// WriteFile writes data to name in the private directory dir, atomically:
// a temporary file (mode 0600) renamed into place.
func WriteFile(dir, name string, data []byte) error {
	if err := Private(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err //nolint:wrapcheck // callers say what the file is
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return err //nolint:wrapcheck // as above
	}
	return nil
}

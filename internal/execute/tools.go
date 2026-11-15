// SPDX-License-Identifier: GPL-3.0-or-later

// Package execute is the only package in fstui that starts processes.
//
// It resolves the absolute paths of the system tools fstui uses once, checks
// that nobody but root could have replaced them, and runs them with an
// argument vector (never a shell) in a fixed environment.
package execute

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Tool is the base name of a system binary fstui runs.
type Tool string

// Tools fstui runs. Add a constant only together with its first caller.
const (
	ZFS   Tool = "zfs"
	Zpool Tool = "zpool"
	// Systemctl lists the scrub timers OpenZFS ships for systemd; it is
	// optional, since not every system runs systemd.
	Systemctl Tool = "systemctl"
	// Tee, Cp and Mv write module parameters and /etc/modprobe.d for
	// the ARC limits; optional, like systemctl.
	Tee Tool = "tee"
	Cp  Tool = "cp"
	Mv  Tool = "mv"
)

// systemDirs are searched in order. PATH from the environment is ignored, so
// a directory the invoking user controls can never supply a tool.
var systemDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// rootUID owns trusted files and directories.
const rootUID = 0

// ErrNotResolved is returned for a tool that Resolve did not resolve,
// such as an optional tool missing on this system.
var ErrNotResolved = errors.New("was not resolved at startup")

// ErrNotFound is returned when a tool is not in any system directory.
var ErrNotFound = errors.New("not found in system directories")

// Tools maps each resolved tool to its absolute path.
type Tools struct {
	paths map[Tool]string
}

// Path returns the absolute path of a resolved tool.
func (t *Tools) Path(tool Tool) (string, bool) {
	p, ok := t.paths[tool]
	return p, ok
}

// Resolve finds each tool in the system directories and checks that it and
// every directory above it are owned by root and writable by nobody else.
func Resolve(tools ...Tool) (*Tools, error) {
	return resolver{dirs: systemDirs, trustedUID: rootUID, top: "/"}.resolve(tools)
}

// ResolveOptional resolves required tools as Resolve does, and each
// optional tool only if it passes the same checks; one that is missing
// or untrusted is left out, and Path reports it as not resolved.
func ResolveOptional(required, optional []Tool) (*Tools, error) {
	r := resolver{dirs: systemDirs, trustedUID: rootUID, top: "/"}
	t, err := r.resolve(required)
	if err != nil {
		return nil, err
	}
	for _, tool := range optional {
		if p, err := r.find(tool); err == nil {
			t.paths[tool] = p
		}
	}
	return t, nil
}

// resolver carries the parameters of Resolve so tests can point it at a
// temporary directory owned by the test user.
type resolver struct {
	dirs       []string
	trustedUID uint32
	top        string // walk directories up to and including this one
}

func (r resolver) resolve(tools []Tool) (*Tools, error) {
	paths := make(map[Tool]string, len(tools))
	for _, tool := range tools {
		p, err := r.find(tool)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", tool, err)
		}
		paths[tool] = p
	}
	return &Tools{paths: paths}, nil
}

func (r resolver) find(tool Tool) (string, error) {
	for _, dir := range r.dirs {
		candidate := filepath.Join(dir, string(tool))
		if _, err := os.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return "", fmt.Errorf("checking %s: %w", candidate, err)
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolving symlinks in %s: %w", candidate, err)
		}
		// Check the path as found and the path it resolves to: a symlink
		// in a trusted directory may point into an untrusted one.
		if err := r.checkDirs(filepath.Dir(candidate)); err != nil {
			return "", err
		}
		if err := r.checkDirs(filepath.Dir(resolved)); err != nil {
			return "", err
		}
		if err := r.checkFile(resolved); err != nil {
			return "", err
		}
		return resolved, nil
	}
	return "", ErrNotFound
}

// checkFile requires a regular, executable file with a trusted owner and no
// group or world write permission.
func (r resolver) checkFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("checking %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return r.checkOwner(path, info)
}

// checkDirs walks from dir up to r.top and checks each directory.
func (r resolver) checkDirs(dir string) error {
	for {
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("checking %s: %w", dir, err)
		}
		if err := r.checkOwner(dir, info); err != nil {
			return err
		}
		if dir == r.top {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("%s is not below %s", dir, r.top)
		}
		dir = parent
	}
}

func (r resolver) checkOwner(path string, info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot read owner", path)
	}
	if st.Uid != r.trustedUID {
		return fmt.Errorf("%s is owned by uid %d, not uid %d", path, st.Uid, r.trustedUID)
	}
	// A symlink always reports mode 0777 on Linux and its permissions are
	// never used (symlink(7)); merged-/usr systems link /usr/sbin to bin.
	// Replacing the link needs write access to its directory, which this
	// walk checks, and find checks the directories it resolves to.
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or others (mode %v)", path, info.Mode().Perm())
	}
	return nil
}

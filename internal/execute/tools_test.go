// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree is a temporary directory laid out like a system bin directory and
// owned by the test user, who plays the part of root.
type tree struct {
	top, bin string
	r        resolver
}

func newTree(t *testing.T) tree {
	t.Helper()
	top := t.TempDir()
	mustChmod(t, top, 0o755)
	bin := filepath.Join(top, "sbin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return tree{top: top, bin: bin, r: resolver{dirs: []string{bin}, trustedUID: uint32(os.Getuid()), top: top}}
}

func writeTool(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, path, mode)
}

func mustChmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func wantErrContaining(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", part)
	}
	if !strings.Contains(err.Error(), part) {
		t.Fatalf("error %q does not contain %q", err, part)
	}
}

func TestResolveFindsTrustedTool(t *testing.T) {
	tr := newTree(t)
	writeTool(t, filepath.Join(tr.bin, "zfs"), 0o755)
	tools, err := tr.r.resolve([]Tool{ZFS})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := tools.Path(ZFS); p != filepath.Join(tr.bin, "zfs") {
		t.Fatalf("Path(zfs) = %q, want %q", p, filepath.Join(tr.bin, "zfs"))
	}
}

func TestResolveUsesFirstDirectoryInOrder(t *testing.T) {
	tr := newTree(t)
	second := filepath.Join(tr.top, "bin")
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, filepath.Join(tr.bin, "zfs"), 0o755)
	writeTool(t, filepath.Join(second, "zfs"), 0o755)
	tr.r.dirs = []string{tr.bin, second}
	tools, err := tr.r.resolve([]Tool{ZFS})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := tools.Path(ZFS); p != filepath.Join(tr.bin, "zfs") {
		t.Fatalf("Path(zfs) = %q, want the first directory's copy", p)
	}
}

func TestResolveRejectsGroupWritableTool(t *testing.T) {
	tr := newTree(t)
	writeTool(t, filepath.Join(tr.bin, "zfs"), 0o775)
	_, err := tr.r.resolve([]Tool{ZFS})
	wantErrContaining(t, err, "writable by group or others")
}

func TestResolveRejectsWorldWritableDirectory(t *testing.T) {
	tr := newTree(t)
	writeTool(t, filepath.Join(tr.bin, "zfs"), 0o755)
	mustChmod(t, tr.bin, 0o777)
	_, err := tr.r.resolve([]Tool{ZFS})
	wantErrContaining(t, err, "writable by group or others")
}

func TestResolveRejectsWritableAncestor(t *testing.T) {
	tr := newTree(t)
	writeTool(t, filepath.Join(tr.bin, "zfs"), 0o755)
	mustChmod(t, tr.top, 0o777)
	_, err := tr.r.resolve([]Tool{ZFS})
	wantErrContaining(t, err, "writable by group or others")
}

func TestResolveRejectsUntrustedOwner(t *testing.T) {
	tr := newTree(t)
	writeTool(t, filepath.Join(tr.bin, "zfs"), 0o755)
	tr.r.trustedUID = uint32(os.Getuid()) + 1
	_, err := tr.r.resolve([]Tool{ZFS})
	wantErrContaining(t, err, "is owned by uid")
}

func TestResolveRejectsSymlinkIntoWritableDirectory(t *testing.T) {
	tr := newTree(t)
	evil := filepath.Join(tr.top, "evil")
	if err := os.Mkdir(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, filepath.Join(evil, "zfs"), 0o755)
	mustChmod(t, evil, 0o777)
	if err := os.Symlink(filepath.Join(evil, "zfs"), filepath.Join(tr.bin, "zfs")); err != nil {
		t.Fatal(err)
	}
	_, err := tr.r.resolve([]Tool{ZFS})
	wantErrContaining(t, err, "writable by group or others")
}

func TestResolveRejectsNonExecutable(t *testing.T) {
	tr := newTree(t)
	writeTool(t, filepath.Join(tr.bin, "zfs"), 0o644)
	_, err := tr.r.resolve([]Tool{ZFS})
	wantErrContaining(t, err, "not executable")
}

func TestResolveReportsMissingTool(t *testing.T) {
	tr := newTree(t)
	_, err := tr.r.resolve([]Tool{Zpool})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	wantErrContaining(t, err, "zpool")
}

// TestResolveSystemZFS checks the real rules against the installed OpenZFS
// tools on hosts that have them.
func TestResolveSystemZFS(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/zfs"); err != nil {
		t.Skip("OpenZFS userland not installed")
	}
	tools, err := Resolve(ZFS, Zpool)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := tools.Path(ZFS); !filepath.IsAbs(p) {
		t.Fatalf("Path(zfs) = %q, want an absolute path", p)
	}
}

// Merged-/usr systems such as current Gentoo make /usr/sbin a symlink to
// bin. A symlink always reports mode 0777 on Linux and its permissions
// are never used (symlink(7)), so a symlinked directory in a trusted
// tree must not be refused for them; its target is checked instead.
func TestResolveFollowsSymlinkedDirectory(t *testing.T) {
	tr := newTree(t)
	target := filepath.Join(tr.top, "bin")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, filepath.Join(target, "zfs"), 0o755)
	linked := filepath.Join(tr.top, "usrsbin")
	if err := os.Symlink("bin", linked); err != nil {
		t.Fatal(err)
	}
	tr.r.dirs = []string{linked}
	tools, err := tr.r.resolve([]Tool{ZFS})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := tools.Path(ZFS); p != filepath.Join(target, "zfs") {
		t.Fatalf("path = %s, want the resolved %s", p, filepath.Join(target, "zfs"))
	}
}

// A symlinked directory whose target is writable by others is still
// refused: the target is checked like any directory.
func TestResolveRejectsSymlinkedDirectoryIntoWritableTarget(t *testing.T) {
	tr := newTree(t)
	target := filepath.Join(tr.top, "open")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, filepath.Join(target, "zfs"), 0o755)
	mustChmod(t, target, 0o777)
	linked := filepath.Join(tr.top, "usrsbin")
	if err := os.Symlink("open", linked); err != nil {
		t.Fatal(err)
	}
	tr.r.dirs = []string{linked}
	_, err := tr.r.resolve([]Tool{ZFS})
	wantErrContaining(t, err, "writable by group or others")
}

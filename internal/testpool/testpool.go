// SPDX-License-Identifier: GPL-3.0-or-later

// Package testpool builds throwaway ZFS pools on sparse files for
// integration tests, and refuses to change anything it did not name.
//
// Every pool it creates is called Prefix plus a random suffix, lives on files
// under Dir, and is destroyed when the test ends, whether it passed or not.
// Every command that changes state first passes its target through Guard.
// Leftovers from a killed test run are removed by scripts/test-clean.sh, which
// matches the same prefix and directory.
//
// The package starts processes itself rather than through internal/execute:
// execute has no mutation entry point yet, and its read timeout must never apply
// to zpool create or destroy. Only test files may import it (depguard), so it is
// never linked into fstui.
package testpool

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Prefix starts the name of every pool this package creates or changes.
const Prefix = "fstuitest_"

// Dir holds the backing files and mountpoints of test pools.
const Dir = "/var/tmp/fstui-test"

// imageSize is the sparse size of each backing file. 256 MiB was too small
// for the golden capture scenarios.
const imageSize = 512 << 20

// suffixBytes of randomness keep concurrent and leftover pools apart.
const suffixBytes = 4

// env matches execute's fixed environment.
var env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}

// Guard returns an error unless name is a pool, dataset, snapshot or
// bookmark inside a pool whose name starts with Prefix and has something
// after it.
func Guard(name string) error {
	pool := name
	if i := strings.IndexAny(pool, "/@#"); i >= 0 {
		pool = pool[:i]
	}
	if len(pool) <= len(Prefix) || !strings.HasPrefix(pool, Prefix) {
		return fmt.Errorf("testpool: refusing to touch %q: its pool %q is not a test pool (prefix %s)", name, pool, Prefix)
	}
	return nil
}

// Pool is a test pool.
type Pool struct {
	Name    string
	Images  []string // backing files, in the order given to zpool create
	created bool
	t       testing.TB
	tools   *execute.Tools
}

// New creates a pool of n sparse files. layout is the vdev type before the
// files ("mirror", "raidz1"), or empty for a plain stripe. The pool is
// destroyed and its files deleted when the test finishes.
func New(t testing.TB, layout string, n int) *Pool {
	t.Helper()
	return NewWith(t, nil, layout, n)
}

// NewWith is New with more zpool create options before the pool name,
// such as "-d" (every feature disabled) or "-o", "compatibility=...".
func NewWith(t testing.TB, opts []string, layout string, n int) *Pool {
	t.Helper()
	p := setup(t)
	t.Cleanup(p.destroy)
	p.makeImages(n)
	args := []string{
		"create",
		// Keep test pools out of /etc/zfs/zpool.cache, so a crash cannot
		// make the host import them at boot.
		"-o", "cachefile=none",
		"-O", "mountpoint=" + filepath.Join(Dir, "mnt", p.Name),
	}
	args = append(append(args, opts...), "--", p.Name)
	if layout != "" {
		args = append(args, layout)
	}
	p.run(execute.Zpool, p.Name, append(args, p.Images...)...)
	p.created = true
	return p
}

// setup checks the caller is root, resolves the tools, names the pool and
// prepares the directories.
func setup(t testing.TB) *Pool {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("testpool: integration tests create pools and must run as root; build them unprivileged and run the binary as root (CONTRIBUTING.md)")
	}
	tools, err := execute.Resolve(execute.ZFS, execute.Zpool)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, suffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	p := &Pool{Name: Prefix + "i" + hex.EncodeToString(suffix), t: t, tools: tools}
	for _, d := range []string{Dir, filepath.Join(Dir, "mnt")} {
		if err := prepareDir(d, 0); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// makeImages creates the pool's n backing files.
func (p *Pool) makeImages(n int) {
	p.t.Helper()
	for i := range n {
		img := filepath.Join(Dir, fmt.Sprintf("%s-%d.img", p.Name, i))
		if err := createSparse(img); err != nil {
			p.t.Fatal(err)
		}
		p.Images = append(p.Images, img)
	}
}

// prepareDir creates dir if it is missing and refuses to use it unless it
// is a real directory owned by uid and writable by nobody else. Dir is in
// /var/tmp, which anyone can write: a directory another user created
// first would let them swap a backing file for a symlink before root
// writes to it.
func prepareDir(dir string, uid uint32) error {
	if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("testpool: %w", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("testpool: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.IsDir():
		return fmt.Errorf("testpool: %s is not a directory (a symlink?); remove it", dir)
	case !ok || st.Uid != uid:
		return fmt.Errorf("testpool: %s is not owned by uid %d; remove it", dir, uid)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("testpool: %s is writable by group or others (mode %v); remove it", dir, fi.Mode().Perm())
	}
	return nil
}

func createSparse(path string) error {
	// path is Dir joined with a name this package generated.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // see above
	if err != nil {
		return fmt.Errorf("creating backing file: %w", err)
	}
	if err := f.Truncate(imageSize); err != nil {
		return errors.Join(fmt.Errorf("sizing %s: %w", path, err), f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}
	return nil
}

// Images makes n sparse backing files and a fresh test pool name, for a
// test whose code under test creates the pool itself (pool creation).
// When the test ends, the pool is destroyed if it exists and the files
// are deleted. Until the pool exists, only Name and Images may be used.
func Images(t testing.TB, n int) *Pool {
	t.Helper()
	p := setup(t)
	t.Cleanup(p.destroy)
	t.Cleanup(func() { // runs first: was the pool made?
		p.created = p.exec(execute.Zpool, p.Name, []string{"list", "-H", "--", p.Name}) == nil
	})
	p.makeImages(n)
	return p
}

// CreateFilesystem creates the filesystem rel inside the pool, e.g.
// "home/user" for <pool>/home/user, and returns its full name.
func (p *Pool) CreateFilesystem(rel string) string {
	p.t.Helper()
	full := p.Name + "/" + rel
	p.run(execute.ZFS, full, "create", "--", full)
	return full
}

// CreateVolume creates a sparse volume of size bytes and returns its full name.
func (p *Pool) CreateVolume(rel string, size uint64) string {
	p.t.Helper()
	full := p.Name + "/" + rel
	p.run(execute.ZFS, full, "create", "-s", "-V", strconv.FormatUint(size, 10), "--", full)
	return full
}

// Snapshot snapshots the dataset rel ("" for the root dataset) and returns
// the snapshot's full name.
func (p *Pool) Snapshot(rel, snap string) string {
	p.t.Helper()
	ds := p.Name
	if rel != "" {
		ds += "/" + rel
	}
	full := ds + "@" + snap
	p.run(execute.ZFS, full, "snapshot", "--", full)
	return full
}

// Offline takes backing file i offline.
func (p *Pool) Offline(i int) {
	p.t.Helper()
	p.run(execute.Zpool, p.Name, "offline", "--", p.Name, p.Images[i])
}

// Corrupt overwrites length bytes of backing file i, starting at offset,
// with random data, behind ZFS's back: the next scrub finds checksum
// errors there. Only the pool's own backing files can be named.
func (p *Pool) Corrupt(i int, offset, length int64) {
	p.t.Helper()
	if err := Guard(p.Name); err != nil || i < 0 || i >= len(p.Images) {
		p.t.Fatalf("testpool: refusing to corrupt image %d of %s: %v", i, p.Name, err)
	}
	f, err := os.OpenFile(p.Images[i], os.O_WRONLY, 0)
	if err != nil {
		p.t.Fatal(err)
	}
	junk := make([]byte, length)
	if _, err := rand.Read(junk); err != nil {
		p.t.Fatal(err)
	}
	if _, err := f.WriteAt(junk, offset); err != nil {
		p.t.Fatal(errors.Join(err, f.Close()))
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		p.t.Fatal(err)
	}
}

// Delegate runs zfs allow with opts on the dataset rel inside the pool
// ("" for the root), e.g. Delegate("home", "-l", "-u", "nobody", "snapshot").
func (p *Pool) Delegate(rel string, opts ...string) {
	p.t.Helper()
	full := p.Name
	if rel != "" {
		full += "/" + rel
	}
	p.run(execute.ZFS, full, append(append([]string{"allow"}, opts...), "--", full)...)
}

// SetProperty sets a property on the dataset rel inside the pool ("" for
// the root).
func (p *Pool) SetProperty(rel, prop, value string) {
	p.t.Helper()
	full := p.Name
	if rel != "" {
		full += "/" + rel
	}
	p.run(execute.ZFS, full, "set", prop+"="+value, "--", full)
}

// Sync writes everything pending to the pool's devices (zpool sync), so
// a following Corrupt hits data that is on disk.
func (p *Pool) Sync() {
	p.t.Helper()
	p.run(execute.Zpool, p.Name, "sync", "--", p.Name)
}

// Scrub runs a scrub and waits for it to finish.
func (p *Pool) Scrub() {
	p.t.Helper()
	p.run(execute.Zpool, p.Name, "scrub", "-w", "--", p.Name)
}

// run checks target, which must be the object args act on, and fails the
// test if the command fails.
func (p *Pool) run(tool execute.Tool, target string, args ...string) {
	p.t.Helper()
	if err := p.exec(tool, target, args); err != nil {
		p.t.Fatal(err)
	}
}

func (p *Pool) exec(tool execute.Tool, target string, args []string) error {
	if err := Guard(target); err != nil {
		return err
	}
	path, ok := p.tools.Path(tool)
	if !ok {
		return fmt.Errorf("testpool: %s was not resolved", tool)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(context.Background(), path, args...) //nolint:gosec // resolved tool, argv only
	cmd.Env, cmd.Dir, cmd.Stderr = env, "/", &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", tool, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// destroy removes the pool, then its backing files. A failed destroy keeps the
// files, so the pool can still be inspected and scripts/test-clean.sh can remove
// it.
func (p *Pool) destroy() {
	if p.created {
		if err := p.destroyRetrying(); err != nil {
			p.t.Errorf("cleanup: %v", err)
			return
		}
	}
	for _, img := range p.Images {
		if err := os.Remove(img); err != nil {
			p.t.Errorf("cleanup: %v", err)
		}
	}
}

// destroyRetrying destroys the pool, trying again while zpool says it
// is busy, as OpenZFS's own tests do (log_must_busy in logapi.shlib: five
// tries, one second apart and doubling): right after heavy writes the
// kernel can hold a pool for a moment after the last file is closed.
func (p *Pool) destroyRetrying() error {
	delay := time.Second
	for try := 1; ; try++ {
		err := p.exec(execute.Zpool, p.Name, []string{"destroy", "-f", "--", p.Name})
		if err == nil || try == busyTries || !strings.Contains(err.Error(), "busy") {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
}

// busyTries is log_must_retry's count for "busy".
const busyTries = 5

// Wait waits for the pool's activities to end, as zpool wait -t takes
// them: "resilver,replace" for an attach or replace to finish.
func (p *Pool) Wait(activities string) {
	p.t.Helper()
	p.run(execute.Zpool, p.Name, "wait", "-t", activities, "--", p.Name)
}

// Destroy destroys the pool now, as the cleanup would; a later pool of
// the same name is destroyed by the cleanup too.
func (p *Pool) Destroy() {
	p.t.Helper()
	p.run(execute.Zpool, p.Name, "destroy", "-f", "--", p.Name)
}

// AllowedDevices are the real block devices a person listed in
// testdata/allowed-devices.conf (CONTRIBUTING.md), which
// scripts/integration.sh passes as FSTUI_ALLOWED_DEVICES. Missing or
// empty means none, and a test that needs one skips. Only
// /dev/disk/by-id paths are accepted.
func AllowedDevices(t testing.TB) []string {
	t.Helper()
	path := os.Getenv("FSTUI_ALLOWED_DEVICES")
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // the list a person wrote, named by the test runner
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case !strings.HasPrefix(line, "/dev/disk/by-id/") || strings.Contains(line, ".."):
			t.Fatalf("testpool: %s lists %q, which is not a /dev/disk/by-id path", path, line)
		default:
			out = append(out, line)
		}
	}
	return out
}

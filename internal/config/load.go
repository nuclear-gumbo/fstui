// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v3"
)

// SystemFile holds site defaults set by packagers or administrators.
const SystemFile = "/etc/fstui/config.yaml"

// maxFileSize bounds what is read from a config file; real ones are a few
// hundred bytes.
const maxFileSize = 1 << 20

// rootUID may own any config file and directory; so may the effective user.
const rootUID = 0

// UserFile returns the user's config.yaml for the effective user. For root
// the home directory comes from the password database, not from $HOME or
// $XDG_CONFIG_HOME, which sudo may carry over from the invoking user; under
// sudo, root's own settings apply.
func UserFile() (string, error) {
	if os.Geteuid() != rootUID {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("finding the user config directory: %w", err)
		}
		return filepath.Join(dir, "fstui", "config.yaml"), nil
	}
	u, err := user.LookupId(strconv.Itoa(rootUID))
	if err != nil {
		return "", fmt.Errorf("finding root's home directory: %w", err)
	}
	return filepath.Join(u.HomeDir, ".config", "fstui", "config.yaml"), nil
}

// DropInDir is the directory of drop-in files next to a config.yaml.
const DropInDir = "config.d"

// Files are the config files in the order they apply, each overriding the
// ones before it key by key: /etc/fstui/config.yaml, the drop-ins in
// /etc/fstui/config.d, the user's config.yaml (userFile; empty when it
// could not be found), then the drop-ins in the user's config.d. Drop-ins
// are the *.yaml files there, in name order; a missing directory has
// none.
func Files(userFile string) []string {
	out := append([]string{SystemFile}, dropIns(filepath.Dir(SystemFile))...)
	if userFile != "" {
		out = append(out, userFile)
		out = append(out, dropIns(filepath.Dir(userFile))...)
	}
	return out
}

// dropIns lists the *.yaml files in dir's config.d, in name order. Each
// is checked like config.yaml when it is read.
func dropIns(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, DropInDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".yaml") && !strings.HasPrefix(name, ".") && !e.IsDir() {
			out = append(out, filepath.Join(dir, DropInDir, name))
		}
	}
	return out // os.ReadDir sorts by name
}

// UserOwned reports whether file is the user's config.yaml or one of its
// drop-ins: a file the Config view may write.
func UserOwned(userFile, file string) bool {
	if userFile == "" {
		return false
	}
	dir := filepath.Dir(userFile)
	return file == userFile || filepath.Dir(file) == filepath.Join(dir, DropInDir)
}

// Target is the file an edit to path is written to: the user's file that
// sets it now, so a later drop-in does not override the edit, else the
// user's config.yaml.
func (c Config) Target(userFile, path string) string {
	if src := c.Sources[path]; UserOwned(userFile, src) {
		return src
	}
	return userFile
}

// Load applies the files in order on top of the defaults. A missing file
// is skipped silently. Every other problem is returned for display and
// never stops loading.
func Load(files ...string) (Config, []Problem) {
	return trust{euid: uint32(os.Geteuid()), top: "/"}.load(files) //nolint:gosec // uids are non-negative
}

// trust carries who may own config files and how far up the directory
// checks go, so tests can use a temporary directory below a world-writable
// /tmp.
type trust struct {
	euid uint32
	top  string
}

func (t trust) load(files []string) (Config, []Problem) {
	c := Default()
	var problems []Problem
	for _, f := range files {
		problems = append(problems, t.loadFile(&c, f)...)
	}
	return c, append(problems, c.check()...)
}

func (t trust) loadFile(c *Config, file string) []Problem {
	data, err := t.read(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []Problem{{File: file, Msg: fmt.Sprintf("ignored: %v", err)}}
	}
	return applyData(c, file, data)
}

// applyData applies one file's contents to c; the Config view uses it
// to check an edited file before saving it.
func applyData(c *Config, file string, data []byte) []Problem {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return []Problem{{File: file, Msg: fmt.Sprintf("ignored, not valid YAML: %v", err)}}
	}
	if len(doc.Content) == 0 {
		return nil // empty file
	}
	a := &applier{file: file}
	a.mapping(c, doc.Content[0], topLevel, "")
	return a.problems
}

// read reads file if neither it nor any directory above it could
// have been written by anyone but euid or root. The file's owner and mode
// are checked on the opened descriptor, so the checked file is the one
// read.
func (t trust) read(file string) ([]byte, error) { return t.readWith(file, nil) }

// readWith is read with one more check of the opened file, if extra is
// set.
func (t trust) readWith(file string, extra func(string, fs.FileInfo) error) ([]byte, error) {
	f, err := os.Open(file) //nolint:gosec // the path is fstui's own config location
	if err != nil {
		return nil, fmt.Errorf("opening config: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only; nothing to flush
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", file, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", file)
	}
	if err := t.check(file, info); err != nil {
		return nil, err
	}
	if extra != nil {
		if err := extra(file, info); err != nil {
			return nil, err
		}
	}
	// Both the directories the path names and those it resolves to: a
	// symbolic link in a directory others can write could be swapped
	// for one to any file root owns.
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", file, err)
	}
	if err := t.checkDirs(filepath.Dir(file)); err != nil {
		return nil, err
	}
	if err := t.checkDirs(filepath.Dir(resolved)); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", file, maxFileSize)
	}
	return data, nil
}

func (t trust) checkDirs(dir string) error {
	for {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("checking %s: %w", dir, err)
		}
		if err := t.check(dir, info); err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if dir == t.top || parent == dir {
			return nil
		}
		dir = parent
	}
}

// check requires the owner to be euid or root and no write permission
// for group or others: otherwise another local user could steer a root
// session, for example by remapping keys.
func (t trust) check(path string, info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot read owner", path)
	}
	if st.Uid != t.euid && st.Uid != rootUID {
		return fmt.Errorf("%s is owned by uid %d, not by uid %d or root", path, st.Uid, t.euid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or others (mode %v)", path, info.Mode().Perm())
	}
	return nil
}

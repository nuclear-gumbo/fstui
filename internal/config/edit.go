// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Edit applies edits (dotted path to value) to the YAML of a config file
// and returns the new document. Comments and the order of existing keys
// are kept; new keys go at the end of their section.
func Edit(current []byte, edits map[string]string) ([]byte, error) {
	doc, root, err := document(current)
	if err != nil {
		return nil, err
	}
	for path, value := range edits {
		if err := setPath(root, strings.Split(path, "."), value); err != nil {
			return nil, err
		}
	}
	return encode(doc)
}

// yamlIndent matches the examples in the documentation.
const yamlIndent = 2

func setPath(m *yaml.Node, path []string, value string) error {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Value != path[0] {
			continue
		}
		if len(path) == 1 {
			v.Kind, v.Tag, v.Value, v.Style, v.Content = yaml.ScalarNode, "", value, 0, nil
			return nil
		}
		if v.Kind != yaml.MappingNode {
			return fmt.Errorf("%s in the current file is not a section", path[0])
		}
		return setPath(v, path[1:], value)
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Value: path[0]}
	if len(path) == 1 {
		m.Content = append(m.Content, key, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
		return nil
	}
	section := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, key, section)
	return setPath(section, path[1:], value)
}

// Parse applies one file's contents on top of the defaults, with the
// same rules as Load but without reading anything from disk.
func Parse(file string, data []byte) (Config, []Problem) {
	c := Default()
	problems := applyData(&c, file, data)
	return c, append(problems, c.check()...)
}

// Check returns the problems of an edited file, so it is validated by
// the same code that loads it.
func Check(file string, data []byte) []Problem {
	_, problems := Parse(file, data)
	return problems
}

// CheckAmong returns the problems of an edited file as fstui would load
// it: files are read in order, with data in place of file, so an alert
// in config.yaml may name a channel a drop-in lists. Problems of the
// other files are left out, and so are problems across files that were
// there before the edit: they are not this edit's to fix.
func CheckAmong(files []string, file string, data []byte) []Problem {
	return trust{euid: uint32(os.Geteuid()), top: "/"}.checkAmong(files, file, data) //nolint:gosec // uids are non-negative
}

func (t trust) checkAmong(files []string, file string, data []byte) []Problem {
	if !slices.Contains(files, file) {
		files = append(slices.Clone(files), file)
	}
	_, before := t.load(files)
	after := Default()
	var out []Problem
	for _, f := range files {
		if f == file {
			out = append(out, applyData(&after, file, data)...)
			continue
		}
		t.loadFile(&after, f) // another file's problems are not this edit's
	}
	for _, p := range after.check() {
		if !slices.Contains(before, p) {
			out = append(out, p)
		}
	}
	return out
}

// Read returns a config file's contents, or nothing if it does not exist.
func Read(file string) ([]byte, error) {
	b, err := os.ReadFile(file) //nolint:gosec // the user's own config file
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	return b, nil
}

// Stage writes data to a temporary copy in the system's temporary
// directory ($TMPDIR, else /tmp), readable only by its owner and created
// with a name nobody else can have prepared, as sudoedit does, and
// returns its path. Nothing replaces file until Promote.
func Stage(file string, data []byte) (string, error) {
	f, err := os.CreateTemp("", "fstui-"+strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))+"-*.yaml")
	if err != nil {
		return "", fmt.Errorf("staging %s: %w", file, err)
	}
	if err := writeAll(f, data); err != nil {
		return "", errors.Join(fmt.Errorf("staging %s: %w", file, err), os.Remove(f.Name()))
	}
	return f.Name(), nil
}

// writeAll writes, syncs and closes f.
func writeAll(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", f.Name(), err)
	}
	return nil
}

// privateDir: ~/.config/fstui holds nothing others need to read.
const privateDir = 0o700

// Backup is where Promote keeps the file it replaces, for Revert.
func Backup(file string) string { return file + ".bak" }

// Promote replaces file with data, the contents the user reviewed,
// keeping the previous file as its backup, and removes the staged copy.
// data comes from memory, not from the staged copy, which sits in a
// temporary directory and could have been changed since it was shown.
// The new file is written beside file and renamed over it, so readers
// see the old file or the new one, never a mix.
func Promote(file, staged string, data []byte) error {
	old, err := Read(file)
	if err != nil {
		return err
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, privateDir); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	// An empty backup records that there was no file before the first
	// save, so revert can say so.
	if err := os.WriteFile(Backup(file), old, 0o600); err != nil {
		return fmt.Errorf("keeping a backup of %s: %w", file, err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(file)+".new-*")
	if err != nil {
		return fmt.Errorf("replacing %s: %w", file, err)
	}
	if err := writeAll(f, data); err != nil {
		return errors.Join(fmt.Errorf("replacing %s: %w", file, err), os.Remove(f.Name()))
	}
	if err := os.Rename(f.Name(), file); err != nil {
		return errors.Join(fmt.Errorf("replacing %s: %w", file, err), os.Remove(f.Name()))
	}
	return Discard(staged)
}

// Discard removes a staged file the user did not confirm.
func Discard(staged string) error {
	if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", staged, err)
	}
	return nil
}

// Diff lists the lines of a and b with "- " for removed, "+ " for added
// and "  " for kept lines (longest common subsequence; config files are
// a few dozen lines).
func Diff(a, b []byte) []string {
	x, y := splitLines(a), splitLines(b)
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			out = append(out, "  "+x[i])
			i, j = i+1, j+1
		case i < len(x) && (j == len(y) || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, "- "+x[i])
			i++
		default:
			out = append(out, "+ "+y[j])
			j++
		}
	}
	return out
}

func splitLines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

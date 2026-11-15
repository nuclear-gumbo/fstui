// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Setting describes one setting the Settings view can edit. keys: and colors:
// are edited in the file.
type Setting struct {
	Path    string   // dotted path in config.yaml, e.g. "thresholds.capacity_warn"
	Desc    string   // one line for the view
	Choices []string // for settings with a fixed set of values; nil for numbers
	Min     int      // for numbers
	Max     int
}

// Settings lists the editable settings; themes lists the theme names to
// offer.
func Settings(themes []string) []Setting {
	onOff := []string{"true", "false"}
	return []Setting{
		{Path: "theme", Desc: "Colour theme", Choices: themes},
		{Path: "units", Desc: "Sizes in IEC units or exact byte counts", Choices: []string{"iec", "exact"}},
		{Path: "ascii", Desc: "Plain characters instead of box and block glyphs; auto on the Linux console", Choices: []string{"auto", "true", "false"}},
		{Path: "mouse", Desc: "Clickable tabs and rows, wheel scrolling", Choices: onOff},
		{Path: "natural_scrolling", Desc: "The wheel moves the content, as on a touchpad set to natural scrolling", Choices: onOff},
		{Path: "superuser", Desc: "Hide advisory warnings; confirmations are always shown", Choices: onOff},
		{Path: "thresholds.capacity_warn", Desc: "Capacity shown as WARN from this percentage", Min: 1, Max: percentMax},
		{Path: "thresholds.capacity_error", Desc: "Capacity shown as FULL from this percentage", Min: 1, Max: percentMax},
		{Path: "thresholds.fragmentation_warn", Desc: "Warn about free-space fragmentation from this percentage", Min: 1, Max: percentMax},
		{Path: "thresholds.scrub_overdue_days", Desc: "Warn when the last scrub is older than this many days", Min: 1, Max: scrubDaysMax},
		{Path: "refresh.pools", Desc: "Seconds between pool reads", Min: refreshPoolsMin, Max: refreshMaxSec},
		{Path: "refresh.datasets", Desc: "Seconds between dataset reads", Min: refreshDSMin, Max: refreshMaxSec},
		{Path: "refresh.arc", Desc: "Seconds between ARC reads while the ARC view is shown", Min: refreshARCMin, Max: refreshMaxSec},
		{Path: "events.retention", Desc: "Events kept in memory", Min: retentionMin, Max: retentionMax},
		{Path: "dashboard.columns", Desc: "Dashboard columns; 0 chooses by the terminal's width", Min: 0, Max: dashboardColumnsMax},
	}
}

// Value returns a setting's current value as it is written in the file.
func (c Config) Value(path string) string {
	switch path {
	case "theme":
		return c.Theme
	case "units":
		return map[Units]string{IEC: "iec", Exact: "exact"}[c.Units]
	case "ascii":
		return map[ASCII]string{ASCIIAuto: "auto", ASCIIOn: "true", ASCIIOff: "false"}[c.ASCII]
	case "mouse":
		return strconv.FormatBool(c.Mouse)
	case "superuser":
		return strconv.FormatBool(c.Superuser)
	case "natural_scrolling":
		return strconv.FormatBool(c.NaturalScrolling)
	case "refresh.arc":
		return strconv.Itoa(int(c.Refresh.ARC.Seconds()))
	case "events.retention":
		return strconv.Itoa(c.Events.Retention)
	case "dashboard.columns":
		return strconv.Itoa(c.Dashboard.Columns)
	case "thresholds.capacity_warn":
		return strconv.Itoa(c.Thresholds.CapacityWarn)
	case "thresholds.capacity_error":
		return strconv.Itoa(c.Thresholds.CapacityError)
	case "thresholds.fragmentation_warn":
		return strconv.Itoa(c.Thresholds.FragmentationWarn)
	case "thresholds.scrub_overdue_days":
		return strconv.Itoa(c.Thresholds.ScrubOverdueDays)
	case "refresh.pools":
		return strconv.Itoa(int(c.Refresh.Pools.Seconds()))
	case "refresh.datasets":
		return strconv.Itoa(int(c.Refresh.Datasets.Seconds()))
	}
	return ""
}

// Edit applies edits (dotted path to value) to the YAML of a config file
// and returns the new document. Comments and the order of existing keys
// are kept; new keys go at the end of their section.
func Edit(current []byte, edits map[string]string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(current, &doc); err != nil {
		return nil, fmt.Errorf("reading the current file: %w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("the current file is not a mapping of settings")
	}
	for path, value := range edits {
		if err := setPath(root, strings.Split(path, "."), value); err != nil {
			return nil, err
		}
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("writing the new file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("writing the new file: %w", err)
	}
	return b.Bytes(), nil
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

// Promote replaces file with the staged copy, keeping the previous file
// as its backup. The copy is written to a temporary file beside file and
// renamed over it, so readers see the old file or the new one, never a
// mix; the staged copy is then removed.
func Promote(file, staged string) error {
	data, err := os.ReadFile(staged) //nolint:gosec // the copy Stage made
	if err != nil {
		return fmt.Errorf("reading %s: %w", staged, err)
	}
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

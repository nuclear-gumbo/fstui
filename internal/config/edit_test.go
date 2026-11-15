// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const userFile = `# my fstui settings
theme: nord   # dark terminal at work
thresholds:
  capacity_warn: 70
`

func TestEditKeepsCommentsAndOrder(t *testing.T) {
	out, err := Edit([]byte(userFile), map[string]string{
		"thresholds.capacity_warn":  "75",
		"thresholds.capacity_error": "85",
		"refresh.pools":             "10",
		"units":                     "exact",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"# my fstui settings", "# dark terminal at work", "capacity_warn: 75", "capacity_error: 85", "refresh:\n  pools: 10", "units: exact"} {
		if !strings.Contains(s, want) {
			t.Errorf("edited file lacks %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "theme:") > strings.Index(s, "thresholds:") {
		t.Errorf("existing keys reordered:\n%s", s)
	}
	c := Default()
	if ps := applyData(&c, "edited", out); len(ps) != 0 || c.Thresholds.CapacityWarn != 75 || c.Units != Exact {
		t.Fatalf("edited file loads as %+v with %v", c.Thresholds, ps)
	}
}

func TestEditEmptyFileAndCheck(t *testing.T) {
	out, err := Edit(nil, map[string]string{"thresholds.capacity_warn": "101"})
	if err != nil {
		t.Fatal(err)
	}
	ps := Check("config.yaml", out)
	if len(ps) != 1 || !strings.Contains(ps[0].Msg, "from 1 to 100") {
		t.Fatalf("problems = %v, want the out-of-range value reported", ps)
	}
}

func TestStagePromoteRevert(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fstui", "config.yaml")
	first, err := Stage(file, []byte("units: exact\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("staging changed the real file")
	}
	if info, _ := os.Stat(first); info.Mode().Perm() != 0o600 {
		t.Errorf("staged file mode %v, want 0600", info.Mode().Perm())
	}
	if err := Promote(file, first); err != nil {
		t.Fatal(err)
	}
	// There was no file: the backup is empty, the defaults.
	if b, err := os.ReadFile(Backup(file)); err != nil || len(b) != 0 {
		t.Fatalf("backup after the first save = %q, %v; want an empty file", b, err)
	}
	second, err := Stage(file, []byte("units: iec\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Promote(file, second); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Backup(file)); string(b) != "units: exact\n" {
		t.Fatalf("backup = %q, want the replaced file", b)
	}
	// Revert: the backup is staged and promoted like any edit, so the
	// revert can itself be reverted.
	bak, _ := Read(Backup(file))
	staged, err := Stage(file, bak)
	if err != nil {
		t.Fatal(err)
	}
	if err := Promote(file, staged); err != nil {
		t.Fatal(err)
	}
	cur, _ := Read(file)
	bak, _ = Read(Backup(file))
	if string(cur) != "units: exact\n" || string(bak) != "units: iec\n" {
		t.Fatalf("after revert: file %q, backup %q", cur, bak)
	}
	discarded, _ := Stage(file, []byte("x: 1\n"))
	if err := Discard(discarded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(discarded); !os.IsNotExist(err) {
		t.Fatal("discard left the staged file")
	}
}

func TestDiff(t *testing.T) {
	got := strings.Join(Diff([]byte("a\nb\nc\n"), []byte("a\nB\nc\nd\n")), "|")
	if want := "  a|- b|+ B|  c|+ d"; got != want {
		t.Fatalf("diff = %q, want %q", got, want)
	}
}

// The pending file is a private copy in the temporary directory, as
// sudoedit makes, and writing it removes the copy.
func TestStageInTempDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	file := filepath.Join(t.TempDir(), "fstui", "config.yaml")
	staged, err := Stage(file, []byte("units: exact\n"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(staged)
	if err != nil || filepath.Dir(staged) != tmp || info.Mode().Perm() != 0o600 {
		t.Fatalf("staged at %s (%v), mode %v", staged, err, info.Mode())
	}
	if err := Promote(file, staged); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "units: exact\n" {
		t.Errorf("written %q", b)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("the temporary copy is still there")
	}
}

// Every setting the view offers must round-trip: an edit to its lowest
// and highest value is accepted on load and read back unchanged.
func TestEverySettingRoundTrips(t *testing.T) {
	for _, s := range Settings([]string{"default"}) {
		values := s.Choices
		if values == nil {
			values = []string{strconv.Itoa(s.Min), strconv.Itoa(s.Max)}
		}
		for _, v := range values {
			out, err := Edit(nil, map[string]string{s.Path: v})
			if err != nil {
				t.Fatalf("%s=%s: %v", s.Path, v, err)
			}
			c, problems := Parse("config.yaml", out)
			if len(problems) > 0 {
				// The capacity thresholds are also checked against each
				// other; that is not this setting's range.
				if strings.Contains(problems[0].String(), "must be below") {
					continue
				}
				t.Fatalf("%s=%s: %v", s.Path, v, problems)
			}
			if got := c.Value(s.Path); got != v {
				t.Errorf("%s=%s read back as %q", s.Path, v, got)
			}
		}
	}
}

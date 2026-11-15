// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// otherUID owns files in the foreign-owner test when it runs as root.
const otherUID = 12345

// testTrust trusts the current user and stops directory checks at dir,
// because t.TempDir() lives under a world-writable /tmp.
func testTrust(dir string) trust {
	return trust{euid: uint32(os.Geteuid()), top: dir} //nolint:gosec // uids are non-negative
}

// write creates name in dir with mode 0600 and returns its path.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func lines(ps []Problem) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.Line
	}
	return out
}

func TestMissingFilesGiveDefaults(t *testing.T) {
	dir := t.TempDir()
	c, ps := testTrust(dir).load([]string{filepath.Join(dir, "absent.yaml")})
	if len(ps) != 0 {
		t.Fatalf("problems for a missing file: %v", ps)
	}
	if want := Default(); c.Theme != want.Theme || c.Thresholds != want.Thresholds || c.Refresh != want.Refresh || c.Units != IEC || c.ASCII != ASCIIAuto {
		t.Fatalf("config = %+v, want defaults", c)
	}
}

func TestFullFile(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", `# every key
theme: nord
colors:
  accent: "#88C0D0"
  warn: { dark: "#EBCB8B", light: "#7A5C00" }
units: exact
ascii: true
mouse: false
superuser: true
thresholds:
  capacity_warn: 70
  capacity_error: 85
  fragmentation_warn: 40
  scrub_overdue_days: 14
refresh:
  pools: 10
  datasets: 30
  arc: 2
`)
	c, ps := testTrust(dir).load([]string{f})
	if len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	want := Thresholds{CapacityWarn: 70, CapacityError: 85, FragmentationWarn: 40, ScrubOverdueDays: 14}
	if c.Theme != "nord" || c.Units != Exact || c.ASCII != ASCIIOn || c.Mouse || !c.Superuser || c.Thresholds != want {
		t.Fatalf("config = %+v", c)
	}
	if c.Refresh != (Refresh{Pools: 10 * time.Second, Datasets: 30 * time.Second, ARC: 2 * time.Second}) {
		t.Fatalf("refresh = %+v", c.Refresh)
	}
	acc := c.Colors[theme.Accent]
	if acc.Dark == nil || acc.Light == nil || acc.Dark.String() != "#88C0D0" || *acc.Light != *acc.Dark {
		t.Fatalf("accent = %+v, want #88C0D0 on both backgrounds", acc)
	}
	if w := c.Colors[theme.Warn]; w.Dark.String() != "#EBCB8B" || w.Light.String() != "#7A5C00" {
		t.Fatalf("warn = %v / %v", w.Dark, w.Light)
	}
}

func TestUserOverridesSystemKeyByKey(t *testing.T) {
	dir := t.TempDir()
	sys := write(t, dir, "system.yaml", "theme: site\nthresholds:\n  capacity_warn: 70\ncolors:\n  warn: \"#111111\"\n")
	usr := write(t, dir, "user.yaml", "theme: mine\ncolors:\n  warn: { light: \"#222222\" }\n")
	c, ps := testTrust(dir).load([]string{sys, usr})
	if len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	if c.Theme != "mine" || c.Thresholds.CapacityWarn != 70 || c.Thresholds.CapacityError != defaultCapacityError {
		t.Fatalf("theme %q, thresholds %+v: want the user's theme and the system's capacity_warn", c.Theme, c.Thresholds)
	}
	if w := c.Colors[theme.Warn]; w.Dark.String() != "#111111" || w.Light.String() != "#222222" {
		t.Fatalf("warn = %v / %v, want the system dark value kept and the user light value", w.Dark, w.Light)
	}
}

func TestBadValuesReportedWithLinesRestApplies(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", `units: metric
mouse: yes
thresholds:
  capacity_warn: 101
  capacity_error: 95
  bogus: 1
refresh:
  pools: 1
superuser: true
colors:
  purple: "#800080"
  ok: "#12345"
`)
	c, ps := testTrust(dir).load([]string{f})
	if got, want := lines(ps), []int{1, 2, 4, 6, 8, 11, 12}; !slices.Equal(got, want) {
		t.Fatalf("problem lines = %v, want %v\n%v", got, want, ps)
	}
	if !c.Superuser || c.Thresholds.CapacityError != 95 || c.Units != IEC || c.Thresholds.CapacityWarn != defaultCapacityWarn {
		t.Fatalf("config = %+v: valid keys must apply, invalid ones keep their defaults", c)
	}
	for _, p := range ps {
		if !strings.HasPrefix(p.String(), f+":") {
			t.Errorf("problem %q does not start with the file name", p)
		}
	}
}

func TestCapacityWarnMustBeBelowError(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", "thresholds:\n  capacity_warn: 95\n")
	c, ps := testTrust(dir).load([]string{f})
	if len(ps) != 1 || !strings.Contains(ps[0].Msg, "capacity_warn (95)") {
		t.Fatalf("problems = %v", ps)
	}
	if c.Thresholds.CapacityWarn != defaultCapacityWarn || c.Thresholds.CapacityError != defaultCapacityError {
		t.Fatalf("thresholds = %+v, want both defaults", c.Thresholds)
	}
}

// ignored asserts that file was not applied and that one problem names it.
func ignored(t *testing.T, tr trust, file, reason string) {
	t.Helper()
	c, ps := tr.load([]string{file})
	if c.Theme != defaultTheme {
		t.Errorf("theme %q from a file that should be ignored", c.Theme)
	}
	if len(ps) != 1 || ps[0].File != file || !strings.Contains(ps[0].Msg, reason) {
		t.Errorf("problems = %v, want one naming %s and %q", ps, file, reason)
	}
}

func TestUnsafeFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	tr := testTrust(dir)

	f := write(t, dir, "groupwritable.yaml", "theme: evil\n")
	if err := os.Chmod(f, 0o620); err != nil {
		t.Fatal(err)
	}
	ignored(t, tr, f, "writable by group or others")

	sub := filepath.Join(dir, "shared")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	f = write(t, sub, "config.yaml", "theme: evil\n")
	if err := os.Chmod(sub, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	ignored(t, tr, f, "writable by group or others")

	f = write(t, dir, "foreign.yaml", "theme: evil\n")
	foreign := tr
	if os.Geteuid() == rootUID {
		if err := os.Chown(f, otherUID, -1); err != nil {
			t.Fatal(err)
		}
	} else {
		foreign.euid++ // the file's owner is now neither the trusted user nor root
	}
	ignored(t, foreign, f, "owned by uid")

	ignored(t, tr, write(t, dir, "broken.yaml", "theme: [unclosed\n"), "not valid YAML")
	ignored(t, tr, write(t, dir, "big.yaml", "theme: evil\n#"+strings.Repeat("x", maxFileSize)+"\n"), "larger than")
}

func TestAliasesRejected(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", "thresholds:\n  capacity_warn: &w 70\n  fragmentation_warn: *w\n")
	c, ps := testTrust(dir).load([]string{f})
	if len(ps) != 1 || ps[0].Line != 3 || !strings.Contains(ps[0].Msg, "aliases") {
		t.Fatalf("problems = %v, want the alias on line 3 refused", ps)
	}
	if c.Thresholds.FragmentationWarn != defaultFragmentationWarn {
		t.Fatalf("fragmentation_warn = %d from an alias", c.Thresholds.FragmentationWarn)
	}
}

func TestHostileValuesEscapedInProblems(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", "theme: \"x\\e[2J\\a\"\nunits: \"\\e]0;pwned\\a\"\n")
	_, ps := testTrust(dir).load([]string{f})
	if len(ps) != 2 {
		t.Fatalf("problems = %v", ps)
	}
	for _, p := range ps {
		if strings.ContainsAny(p.String(), "\x1b\x07") {
			t.Errorf("problem contains raw control characters: %q", p.String())
		}
	}
}

func TestKeys(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "config.yaml", "keys:\n  select: enter\n  left: [h, left]\n  up: {k: 1}\n")
	c, ps := testTrust(dir).load([]string{f})
	if len(ps) != 1 || ps[0].Line != 4 {
		t.Fatalf("problems = %v, want one for the mapping on line 4", ps)
	}
	if strings.Join(c.Keys["select"], ",") != "enter" || strings.Join(c.Keys["left"], ",") != "h,left" {
		t.Fatalf("keys = %v", c.Keys)
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// Every shipped theme parses cleanly, names its source, defines a dark
// set, and passes the readability and distinctness checks a user theme
// would be warned about.
func TestShippedThemes(t *testing.T) {
	for _, name := range theme.BuiltinNames()[1:] {
		th, ps := testTrust(t.TempDir()).loadTheme(name, nil)
		if len(ps) != 0 {
			t.Errorf("%s: %v", name, ps)
		}
		if th.Name != name || th.Source == "" || th.Dark == nil || th.Dark.Background == nil {
			t.Errorf("%s: name %q, source %q, dark %v", name, th.Name, th.Source, th.Dark)
		}
		for tok := theme.Token(0); th.Dark != nil && tok <= theme.Border; tok++ {
			if th.Dark.Colors[tok] == nil {
				t.Errorf("%s: dark set lacks %s", name, tok)
			}
		}
	}
}

func TestThemeLookupOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "gruvbox.yaml", "name: gruvbox\nsource: mine\ndark:\n  accent: \"#010203\"\n")
	th, ps := testTrust(dir).loadTheme("gruvbox", []string{dir})
	if len(ps) != 0 || th.Source != "mine" || th.Dark.Colors[theme.Accent].String() != "#010203" {
		t.Fatalf("a user theme should shadow the shipped one: %+v %v", th, ps)
	}
	th, ps = testTrust(dir).loadTheme("absent", []string{dir})
	if th.Name != theme.DefaultName || len(ps) != 1 || !strings.Contains(ps[0].Msg, `theme "absent" not found`) {
		t.Fatalf("missing theme: %+v %v", th, ps)
	}
	if th, ps := testTrust(dir).loadTheme(theme.DefaultName, []string{dir}); th.Name != theme.DefaultName || ps != nil {
		t.Fatalf("default: %+v %v", th, ps)
	}
}

func TestThemeProblems(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "bad.yaml", `name: bad
colour: blue
dark:
  background: "#000000"
  accent: "#zzzzzz"
  purple: "#800080"
  ok: "#00aa00"
  warn: "#00ab00"
  error: "#ff0000"
  muted: "#111111"
  meter: ["#000000", "#ffffff"]
`)
	th, ps := testTrust(dir).loadTheme("bad", []string{dir})
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.String())
	}
	got := strings.Join(msgs, "\n")
	for _, want := range []string{
		`bad.yaml:2: unknown key "colour"`,
		`bad.yaml:5: dark.accent: colour "#zzzzzz" is not #RRGGBB`,
		`bad.yaml:6: dark: unknown token "purple"`,
		`bad.yaml:11: dark.meter: want three colours`,
		"dark: ok and warn are hard to tell apart",
		"dark.muted #111111 is hard to read on #000000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("problems lack %q:\n%s", want, got)
		}
	}
	if th.Dark.Colors[theme.Error].String() != "#FF0000" || th.Dark.Colors[theme.Accent] != nil {
		t.Errorf("valid entries must apply and invalid ones not: %+v", th.Dark.Colors)
	}
}

func TestUnsafeThemeIgnored(t *testing.T) {
	dir := t.TempDir()
	f := write(t, dir, "nord.yaml", "name: nord\ndark:\n  accent: \"#ff0000\"\n")
	if err := os.Chmod(f, 0o666); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	th, ps := testTrust(dir).loadTheme("nord", []string{dir})
	if th.Name != theme.DefaultName || len(ps) != 1 || !strings.Contains(ps[0].Msg, "writable by group or others") {
		t.Fatalf("a world-writable theme must be ignored, not fall through to the shipped one: %+v %v", th, ps)
	}
}

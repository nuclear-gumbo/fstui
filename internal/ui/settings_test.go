// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/theme"
)

func newSettings(t *testing.T, content string) (*Settings, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fstui", "config.yaml")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, ps := parseFiles(file)
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	s := NewSettings(file, []string{file}, cfg, []string{"default", "gruvbox"}, parseFiles,
		func(n string) (theme.Theme, []config.Problem) { return theme.Theme{Name: n}, nil })
	return s, file
}

// parseFiles stands in for config.Load, whose ownership walk refuses
// files under a world-writable /tmp; the parsing rules are the same.
func parseFiles(files ...string) (config.Config, []config.Problem) {
	b, err := config.Read(files[len(files)-1])
	if err != nil {
		return config.Config{}, []config.Problem{{Msg: err.Error()}}
	}
	return config.Parse(files[len(files)-1], b)
}

func k(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// to opens the group of path and selects it, as the keys would.
func (s *Settings) to(path string) {
	s.open(config.SectionOf(path))
	for i, set := range s.list {
		if set.Path == path {
			s.cursor = i
		}
	}
}

func TestSettingsChoicesAndNumbers(t *testing.T) {
	s, _ := newSettings(t, "")
	s.to("units")
	s.Update(k("enter"))
	if s.pending["units"] != "exact" || s.Status().Figure != "1 unsaved change" {
		t.Fatalf("pending %v, figure %q", s.pending, s.Status().Figure)
	}
	s.Update(k("enter")) // back to iec, the loaded value: nothing pending
	if len(s.pending) != 0 {
		t.Fatalf("cycling back left %v pending", s.pending)
	}
	s.to("thresholds.capacity_warn")
	s.Update(k("enter"))
	for _, key := range []string{"backspace", "backspace", "1", "0", "1", "enter"} {
		s.Update(k(key))
	}
	if !strings.Contains(s.message, "from 1 to 100") || s.input == nil {
		t.Fatalf("101 accepted: message %q", s.message)
	}
	s.Update(k("backspace"))
	s.Update(k("enter"))
	if s.pending["thresholds.capacity_warn"] != "10" || s.input != nil {
		t.Fatalf("pending %v", s.pending)
	}
}

// Changes are staged in a temporary file and shown as a diff; only a
// confirmation replaces config.yaml, keeping a backup that revert
// restores.
func TestSettingsReviewSaveRevert(t *testing.T) {
	const original = "# mine\ntheme: gruvbox\n"
	s, file := newSettings(t, original)
	s.to("superuser")
	s.Update(k("enter"))
	s.Update(k("p"))
	if s.review == nil {
		t.Fatalf("no review: %q", s.message)
	}
	staged := s.review.files[0].staged
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged file: %v", err)
	}
	out := ansi.Strip(s.Render(Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.ASCII, nil)}, 100, 30))
	if !strings.Contains(out, "+ superuser: true") || !strings.Contains(out, "  # mine") || !strings.Contains(out, "temporary copy: ") {
		t.Fatalf("review:\n%s", out)
	}
	s.Update(k("n"))
	if b, _ := os.ReadFile(file); string(b) != original {
		t.Fatalf("n changed the file: %q", b)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("n left the staged file")
	}

	s.Update(k("p"))
	if cmd := s.Update(k("y")); cmd != nil || s.review == nil {
		t.Fatal("y wrote the file; w writes it now")
	}
	cmd := s.Update(k("w"))
	if cmd == nil {
		t.Fatal("saving sent no settings to the views")
	}
	msg, ok := cmd().(ConfigMsg)
	if !ok || !msg.Config.Superuser || msg.Theme.Name != "gruvbox" {
		t.Fatalf("ConfigMsg = %+v", msg)
	}
	if b, _ := os.ReadFile(config.Backup(file)); string(b) != original {
		t.Fatalf("backup = %q", b)
	}
	if s.sourceOf("superuser") != file {
		t.Fatalf("source of superuser = %q", s.sourceOf("superuser"))
	}

	s.Update(k("u"))
	if s.review == nil || !s.review.revert {
		t.Fatalf("u did not offer a revert: %q", s.message)
	}
	s.Update(k("w"))
	if b, _ := os.ReadFile(file); string(b) != original {
		t.Fatalf("after revert: %q", b)
	}
}

func TestSettingsRefusesInvalidFile(t *testing.T) {
	s, file := newSettings(t, "")
	s.pending["thresholds.capacity_warn"] = "95" // above capacity_error (90)
	s.Update(k("p"))
	if s.review == nil || len(s.review.problems) == 0 {
		t.Fatal("an invalid file was offered for saving without problems")
	}
	s.Update(k("w"))
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("y wrote a file with problems")
	}
}

func TestSettingsIsViewNine(t *testing.T) {
	s, _ := newSettings(t, "")
	m := newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"}, configOf(s))
	m, _ = press(t, m, "9")
	if m.active != 1 {
		t.Fatal("9 did not open Settings")
	}
	m, _ = press(t, m, "2")
	if m.active != 1 {
		t.Fatal("2 opened Settings, which claims 9")
	}
}

func TestSettingsFlagNotEditable(t *testing.T) {
	s, _ := newSettings(t, "")
	s.cfg.Superuser, s.cfg.Sources["superuser"] = true, config.FlagSource
	s.to("superuser")
	s.Update(k("enter"))
	if len(s.pending) != 0 || !strings.Contains(s.message, "command line") {
		t.Fatalf("pending %v, message %q", s.pending, s.message)
	}
}

// Settings reads nothing from the system, so the status bar shows no
// refresh age. The tab lists the groups first; a group shows its
// settings by name, not by key, with the selected one explained.
func TestSettingsLayout(t *testing.T) {
	s, _ := newSettings(t, "")
	m := newModel(colorprofile.ASCII, configOf(s))
	if got := ansi.Strip(m.statusBar(s.Status())); strings.Contains(got, "loading") || strings.Contains(got, "refreshed") {
		t.Errorf("status bar: %q", got)
	}
	out := ansi.Strip(s.Render(m.display, 100, 20))
	for _, want := range []string{"Display", "Warnings", "Tune", "enter opens one"} {
		if !strings.Contains(out, want) {
			t.Errorf("groups lack %q:\n%s", want, out)
		}
	}
	s.Update(k("j"))
	s.Update(k("enter"))
	out = ansi.Strip(s.Render(m.display, 100, 20))
	if !strings.Contains(out, "Capacity warning at (%)") || !strings.Contains(out, "thresholds.capacity_warn in the file") || strings.Contains(out, "Colour theme") {
		t.Errorf("Warnings group:\n%s", out)
	}
	s.Update(k("esc"))
	if s.section != -1 || s.Nested() {
		t.Errorf("esc left the group open: section %d", s.section)
	}
}

// A group longer than the screen scrolls: the selected setting is always
// drawn, down to the last (the list did not scroll in 0.8.0).
func TestSettingsScroll(t *testing.T) {
	s, _ := newSettings(t, "")
	m := newModel(colorprofile.ASCII, configOf(s))
	s.open(len(config.Sections) - 1) // Tune, the longest
	n := len(s.items())
	for i := range n {
		out := ansi.Strip(s.Render(m.display, 100, 16))
		label := s.list[s.cursor].Label()
		if !strings.Contains(out, "> "+label) {
			t.Fatalf("row %d of %d (%s) not drawn:\n%s", i, n, label, out)
		}
		if lines := strings.Count(out, "\n") + 1; lines > 16 {
			t.Fatalf("%d lines in 16", lines)
		}
		s.Update(k("j"))
	}
	if !strings.Contains(ansi.Strip(s.Render(m.display, 100, 16)), "Incompressible file below") {
		t.Error("the last setting is not reachable")
	}
}

// Text settings take letters and punctuation: a host name, a time.
func TestSettingsTypeText(t *testing.T) {
	s, _ := newSettings(t, "")
	s.to("schedules.cron_time")
	s.Update(k("enter"))
	for range 5 {
		s.Update(k("backspace"))
	}
	for _, c := range []string{"0", "4", ":", "3", "0"} {
		s.Update(k(c))
	}
	s.Update(k("enter"))
	if s.pending["schedules.cron_time"] != "04:30" {
		t.Errorf("pending %v, message %q", s.pending, s.message)
	}
}

// Digits, b, q and ? are global keys, but while a number is being typed
// they belong to the field: typing 75 must not switch to view 7 or 5.
func TestSettingsTypingThroughRoot(t *testing.T) {
	s, _ := newSettings(t, "")
	m := newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"}, configOf(s))
	m, _ = press(t, m, "9")
	s.to("thresholds.capacity_warn")
	m, _ = press(t, m, "enter")
	for _, k := range []string{"backspace", "backspace", "1", "7", "b", "?", "q"} {
		m, _ = press(t, m, k)
	}
	if m.active != 1 || m.help || m.display.Exact || s.input == nil || s.input.text != "17" {
		t.Fatalf("active %d, help %v, exact %v, input %+v", m.active, m.help, m.display.Exact, s.input)
	}
	for _, b := range s.Keys() {
		if b.Help().Desc == "cancel" && b.Help().Key != "esc" {
			t.Errorf("cancel hint while typing: %q", b.Help().Key)
		}
	}
	m, _ = press(t, m, "esc")
	if s.input != nil || m.active != 1 {
		t.Fatalf("esc did not cancel the field: input %+v, active %d", s.input, m.active)
	}
}

// themeOf runs a command and returns the theme it previews, or "".
func themeOf(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	msg, ok := cmd().(ThemeMsg)
	if !ok {
		t.Fatalf("command sent %T, not a theme preview", cmd())
	}
	return msg.Theme.Name
}

// Choosing a theme shows it at once; discarding it, or leaving Settings
// with it unsaved, shows the saved theme again; coming back shows the
// unsaved choice.
func TestSettingsThemePreview(t *testing.T) {
	s, _ := newSettings(t, "")
	s.to("theme")
	cmd := s.Update(k("enter"))
	if got := themeOf(t, cmd); got != "gruvbox" {
		t.Fatalf("choosing previewed %q", got)
	}
	if got := themeOf(t, s.Deactivate()); got != "default" {
		t.Errorf("leaving showed %q", got)
	}
	if got := themeOf(t, s.Activate()); got != "gruvbox" {
		t.Errorf("coming back showed %q", got)
	}
	cmd = s.Update(k("d"))
	if got := themeOf(t, cmd); got != "default" || len(s.pending) != 0 {
		t.Errorf("discarding showed %q, pending %v", got, s.pending)
	}
	if s.Deactivate() != nil || s.Activate() != nil {
		t.Error("previewed with nothing unsaved")
	}
}

func TestRootAppliesThemePreview(t *testing.T) {
	m := newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"})
	next, _ := m.Update(ThemeMsg{Theme: theme.Theme{Name: "gruvbox"}})
	if nm, ok := next.(Model); !ok || nm.opts.Theme.Name != "gruvbox" {
		t.Error("the root did not apply the previewed theme")
	}
}

// Without a user config file, nothing is staged or saved, and the system
// file is never edited in its place.
func TestSettingsWithoutAUserFile(t *testing.T) {
	cfg := config.Default()
	s := NewSettings("", []string{config.SystemFile}, cfg, []string{"default"}, parseFiles, nil)
	s.change(s.list[0])
	if len(s.pending) != 0 || !strings.Contains(s.message, "cannot be saved") {
		t.Errorf("pending %v, message %q", s.pending, s.message)
	}
	s.startReview()
	s.startRevert()
	if s.review != nil {
		t.Error("a review was started without a user file")
	}
	_ = s.Render(asciiDisplay(true), 80, 24)
}

// configOf is view 9 with s as its General tab.
func configOf(s *Settings) *Config {
	return NewConfig(s, NewNotifications(s.file, s.cfg, func() (config.Secrets, []config.Problem) { return nil, nil }, nil, nil), NewAlerts(s.file, s.cfg, nil), NewSchedules(nil, "", time.Minute, time.Now))
}

// An edit goes to the user's file that sets the key: here a drop-in for
// the theme and config.yaml for the units, reviewed and written together.
func TestSettingsEditsGoToTheirFile(t *testing.T) {
	s, file := newSettings(t, "units: iec\n")
	drop := filepath.Join(filepath.Dir(file), config.DropInDir, "theme.yaml")
	if err := os.MkdirAll(filepath.Dir(drop), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drop, []byte("theme: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cfg.Sources["theme"] = drop
	s.pending = map[string]string{"theme": "gruvbox", "units": "exact"}
	s.Update(k("p"))
	if s.review == nil || len(s.review.files) != 2 {
		t.Fatalf("review: %+v, %s", s.review, s.message)
	}
	s.Update(k("w"))
	if b, _ := os.ReadFile(drop); string(b) != "theme: gruvbox\n" {
		t.Errorf("drop-in: %q", b)
	}
	if b, _ := os.ReadFile(file); string(b) != "units: exact\n" {
		t.Errorf("config.yaml: %q", b)
	}
	if len(s.lastSaved) != 2 {
		t.Errorf("last saved %v", s.lastSaved)
	}
}

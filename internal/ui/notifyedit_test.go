// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// editTab is the tab over a real config.yaml and secrets.yaml in a
// temporary directory.
func editTab(t *testing.T) (*Notifications, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("theme: gruvbox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir()) // where the review stages config.yaml
	load := func() (config.Secrets, []config.Problem) {
		b, _ := os.ReadFile(filepath.Join(dir, config.SecretsName))
		s := config.Secrets{}
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if k, v, ok := strings.Cut(l, ": "); ok {
				s[k] = strings.Trim(v, `"`)
			}
		}
		return s, nil
	}
	return NewNotifications(file, config.Default(), load, nil, nil), dir
}

// typeInto types s into the form's field labelled label, as a person
// does: the focused field takes typing at once.
func typeInto(t *testing.T, n *Notifications, label, s string) {
	t.Helper()
	for i, f := range n.form.fields() {
		if f.label == label {
			n.form.cursor = i
			for range []rune(n.form.values[f.key]) { // the field holds its current value
				n.Update(k("backspace"))
			}
			for _, r := range s {
				n.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			return
		}
	}
	t.Fatalf("no field %q", label)
}

// finish ends the form with its edits and reviews them, as ctrl+enter
// then p do.
func finish(n *Notifications) {
	n.Update(k("ctrl+enter"))
	n.Update(k("p"))
}

// A channel added in the form is written to config.yaml, its secret to
// secrets.yaml (mode 0600), and the review names the secret, never shows it.
func TestAddChannel(t *testing.T) {
	n, dir := editTab(t)
	n.Update(k("a"))
	if n.form == nil {
		t.Fatal("a did not open the form")
	}
	typeInto(t, n, "Name", "phone")
	typeInto(t, n, "Server", "https://ntfy.example.org")
	typeInto(t, n, "Topic", "zfs")
	typeInto(t, n, "Password or token", "tk_s3cret")
	if out := ansi.Strip(n.Render(asciiDisplay(false), 100, 30)); strings.Contains(out, "tk_s3cret") || !strings.Contains(out, "*********") {
		t.Fatalf("form shows the secret, or not that it is set:\n%s", out)
	}
	finish(n)
	if n.review == nil {
		t.Fatalf("p did not review: %s", n.message)
	}
	out := ansi.Strip(n.Render(asciiDisplay(false), 100, 30))
	if !strings.Contains(out, "+     - name: phone") || !strings.Contains(out, "phone: set") || strings.Contains(out, "tk_s3cret") {
		t.Fatalf("review:\n%s", out)
	}
	cmd := n.Update(k("w"))
	if cmd == nil {
		t.Fatalf("w wrote nothing: %s", n.message)
	}
	if _, ok := cmd().(savedMsg); !ok {
		t.Error("no savedMsg")
	}
	c, problems := config.Parse("config.yaml", mustRead(t, filepath.Join(dir, "config.yaml")))
	if len(problems) != 0 || len(c.Notifications.Channels) != 1 || c.Notifications.Channels[0].Topic != "zfs" || c.Theme != "gruvbox" {
		t.Errorf("config.yaml: %+v %v", c.Notifications.Channels, problems)
	}
	fi, err := os.Stat(filepath.Join(dir, config.SecretsName))
	if err != nil || fi.Mode().Perm() != 0o600 || !strings.Contains(string(mustRead(t, filepath.Join(dir, config.SecretsName))), "tk_s3cret") {
		t.Errorf("secrets.yaml: %v %v", fi, err)
	}
}

// A channel that would not load cannot be saved.
func TestIncompleteChannelIsRefused(t *testing.T) {
	n, _ := editTab(t)
	n.Update(k("a"))
	typeInto(t, n, "Name", "half")
	finish(n)
	if n.review == nil || len(n.review.problems) == 0 {
		t.Fatal("an ntfy channel without url and topic reached a clean review")
	}
	if cmd := n.Update(k("w")); cmd != nil {
		t.Error("w saved a channel with problems")
	}
}

// Renaming moves the secret; d twice removes a channel and its secret.
func TestRenameAndRemove(t *testing.T) {
	n, dir := editTab(t)
	n.Update(k("a"))
	typeInto(t, n, "Name", "phone")
	typeInto(t, n, "Server", "https://ntfy.example.org")
	typeInto(t, n, "Topic", "zfs")
	typeInto(t, n, "Password or token", "tok")
	finish(n)
	n.Update(k("w"))
	c, _ := config.Parse("config.yaml", mustRead(t, filepath.Join(dir, "config.yaml")))
	n.Update(ConfigMsg{Config: c})

	n.Update(k("enter"))
	typeInto(t, n, "Name", "mobile")
	finish(n)
	if got := strings.Join(n.review.secrets, ","); got != "mobile: set,phone: removed" {
		t.Errorf("rename: %s", got)
	}
	n.Update(k("w"))
	secrets := string(mustRead(t, filepath.Join(dir, config.SecretsName)))
	if !strings.Contains(secrets, "mobile:") || strings.Contains(secrets, "phone:") {
		t.Errorf("after rename:\n%s", secrets)
	}
	c, _ = config.Parse("config.yaml", mustRead(t, filepath.Join(dir, "config.yaml")))
	n.Update(ConfigMsg{Config: c})

	n.Update(k("d"))
	if len(n.channels()) != 1 || !strings.Contains(n.message, "again") {
		t.Fatal("one d removed the channel")
	}
	n.Update(k("d"))
	n.Update(k("p"))
	n.Update(k("w"))
	if s := string(mustRead(t, filepath.Join(dir, config.SecretsName))); strings.Contains(s, "mobile") {
		t.Errorf("secret kept after removal:\n%s", s)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The form works as every fstui form: enter moves on, letters typed on a
// choice pick by first letter and never step it, and ctrl+enter finishes.
func TestChannelFormKeys(t *testing.T) {
	n, _ := editTab(t)
	n.Update(k("a"))
	for _, r := range "phone" {
		n.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	n.Update(k("enter"))
	if f := n.form.fields()[n.form.cursor]; f.key != "type" {
		t.Fatalf("enter went to %s", f.key)
	}
	for _, r := range "hl" {
		n.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := n.form.values["type"]; got != "ntfy" {
		t.Errorf("h and l changed the type to %s", got)
	}
	n.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if got := n.form.values["type"]; got != "slack" && got != "smtp" {
		t.Errorf("s picked %s", got)
	}
	if n.form.values["name"] != "phone" {
		t.Errorf("name %q", n.form.values["name"])
	}
	n.Update(k("ctrl+enter"))
	if n.form != nil || n.edit == nil {
		t.Error("ctrl+enter did not finish the form with its edits")
	}
}

// After w, the tab is drawn before the saved settings come back; the
// channel just added, under the cursor, is drawn with them (0.8 crashed
// here when a second channel was added).
func TestAddSecondChannel(t *testing.T) {
	n, _ := editTab(t)
	n.cfg.Notifications.Channels = []config.Channel{{Name: "one", Type: "webhook", URL: "https://w"}}
	n.Update(k("a"))
	typeInto(t, n, "Name", "two")
	typeInto(t, n, "Server", "https://ntfy.example.org")
	typeInto(t, n, "Topic", "zfs")
	finish(n)
	if n.Update(k("w")) == nil {
		t.Fatalf("w wrote nothing: %s", n.message)
	}
	if out := ansi.Strip(n.Render(asciiDisplay(false), 100, 30)); !strings.Contains(out, "two") {
		t.Errorf("the new channel is not shown:\n%s", out)
	}
}

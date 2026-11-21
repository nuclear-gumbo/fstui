// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotifications(t *testing.T) {
	c, problems := loadYAML(t, `
notifications:
  repeat: 6h
  resolved: false
  events: on
  events_min: warn
  channels:
    - name: phone
      type: ntfy
      url: https://ntfy.example.org
      topic: zfs
      user: alice
      min_severity: error
    - name: mail
      type: smtp
      host: mail.example.org
      port: 465
      from: fstui@example.org
      to: [ops@example.org, root@example.org]
    - name: lan
      type: gotify
      url: http://gotify.lan
      insecure_http: true
    - name: tg
      type: telegram
      chat_id: -1001234
`)
	if len(problems) != 0 {
		t.Fatalf("%v", problems)
	}
	n := c.Notifications
	if n.Repeat != 6*time.Hour || n.Resolved || n.Events != "on" || n.EventsMin != "warn" || len(n.Channels) != 4 {
		t.Fatalf("%+v", n)
	}
	if ch := n.Channels[0]; ch.Topic != "zfs" || ch.User != "alice" || ch.MinSeverity != "error" {
		t.Errorf("ntfy %+v", ch)
	}
	if ch := n.Channels[1]; ch.Port != 465 || len(ch.To) != 2 || ch.MinSeverity != "warning" {
		t.Errorf("smtp %+v", ch)
	}
	if n.Channels[3].ChatID != "-1001234" {
		t.Errorf("telegram %+v", n.Channels[3])
	}
	if d := Default().Notifications; d.Repeat != 24*time.Hour || !d.Resolved || d.Events != "auto" {
		t.Errorf("defaults %+v", d)
	}
}

// A channel with a problem is reported with its line and left out; the
// others still apply.
func TestNotificationChannelProblems(t *testing.T) {
	c, problems := loadYAML(t, `
notifications:
  repeat: soon
  channels:
    - {name: ok, type: webhook, url: "https://hooks.example.org/x"}
    - {name: Bad Name, type: webhook, url: "https://x"}
    - {name: plain, type: webhook, url: "http://hooks.example.org"}
    - {name: half, type: ntfy, url: "https://ntfy.example.org"}
    - {name: ok, type: slack}
    - {name: what, type: pigeon}
    - {name: mail, type: smtp, host: m, port: 70000, from: a@b, to: c@d}
`)
	var msgs []string
	for _, p := range problems {
		msgs = append(msgs, p.Msg)
	}
	got := strings.Join(msgs, "\n")
	for _, want := range []string{
		"repeat: want a duration", "[1]: name:", "[2]: url: http:// needs insecure_http", "[3]: a ntfy channel needs topic",
		`[4]: a channel named "ok" is already listed`, "[5]: type: want one of", "[6]: port: want a whole number from 1 to 65535",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q in:\n%s", want, got)
		}
	}
	if len(c.Notifications.Channels) != 1 || c.Notifications.Channels[0].Name != "ok" {
		t.Errorf("channels %+v", c.Notifications.Channels)
	}
}

func writeSecret(t *testing.T, dir, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, SecretsName)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// Secrets are read only from a file nobody else can read, and a problem
// never quotes a secret.
func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	tr := testTrust(dir)
	p := writeSecret(t, dir, "phone: s3cret-token\nmail: \"pa ss\"\n", 0o600)
	s, problems := tr.loadSecrets([]string{p, filepath.Join(dir, "missing.yaml")})
	if len(problems) != 0 || s["phone"] != "s3cret-token" || s["mail"] != "pa ss" {
		t.Fatalf("%v %v", s, problems)
	}
	if v, err := tr.secret(s, Channel{Name: "phone"}); err != nil || v != "s3cret-token" {
		t.Errorf("%q %v", v, err)
	}
	if _, err := tr.secret(s, Channel{Name: "none"}); !errors.Is(err, ErrNoSecret) {
		t.Errorf("missing: %v", err)
	}

	other := t.TempDir()
	open := writeSecret(t, other, "phone: x\n", 0o644)
	if s, problems := testTrust(other).loadSecrets([]string{open}); len(s) != 0 || len(problems) != 1 || !strings.Contains(problems[0].Msg, "must be 0600") {
		t.Errorf("readable by others: %v %v", s, problems)
	}

	badDir := t.TempDir()
	bad := writeSecret(t, badDir, "phone: [unclosed s3cret-token\n", 0o600)
	_, problems = testTrust(badDir).loadSecrets([]string{bad})
	if len(problems) != 1 || strings.Contains(problems[0].Msg, "s3cret") || problems[0].Msg != "not YAML" {
		t.Errorf("broken file: %+v", problems)
	}

	sfDir := t.TempDir()
	sf := write(t, sfDir, "token", "from-file\n")
	if v, err := testTrust(sfDir).secret(Secrets{}, Channel{Name: "x", SecretFile: sf}); err != nil || v != "from-file" {
		t.Errorf("secret_file: %q %v", v, err)
	}
}

// loadYAML loads one config file with the given content.
func loadYAML(t *testing.T, content string) (Config, []Problem) {
	t.Helper()
	dir := t.TempDir()
	return testTrust(dir).load([]string{write(t, dir, "config.yaml", content)})
}

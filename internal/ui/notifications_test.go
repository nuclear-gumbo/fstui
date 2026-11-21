// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/schedule"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"

	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

func notificationsTab(t *testing.T, send Tester) *Notifications {
	t.Helper()
	dir := t.TempDir()
	secrets := func() (config.Secrets, []config.Problem) { return config.Secrets{"phone": "s3cret"}, nil }
	cfg := config.Default()
	cfg.Notifications.Channels = []config.Channel{
		{Name: "phone", Type: "ntfy", URL: "https://ntfy.example.org", Topic: "zfs", User: "alice", MinSeverity: "warning"},
		{Name: "chat", Type: "slack", MinSeverity: "error"},
		{Name: "hook", Type: "webhook", URL: "https://hooks.example.org/x", MinSeverity: "warning"},
	}
	return NewNotifications(filepath.Join(dir, "config.yaml"), cfg, secrets, func() []string { return []string{"ZED is not running."} }, send)
}

// The table says whether each channel has its secret, never the secret.
func TestNotificationsTable(t *testing.T) {
	n := notificationsTab(t, nil)
	out := ansi.Strip(n.Render(asciiDisplay(false), 100, 30))
	for _, want := range []string{
		"Getting notified", "[x] Where to send: 3 channels", "[x] What to send: 8 alerts on", "fstui cannot read this system's schedules", "ZED is not running.",
		"phone  ntfy     ntfy.example.org/zfs", "set", "chat   slack    slack webhook", "missing", "hook   webhook  hooks.example.org", "not needed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") {
		t.Error("the secret is shown")
	}
}

// t sends a test message to the selected channel and shows the result.
func TestNotificationsTest(t *testing.T) {
	var got string
	n := notificationsTab(t, func(_ context.Context, _ config.Notifications, ch config.Channel, s config.Secrets, _ string) error {
		got = ch.Name
		if v, _ := s.Secret(ch); v != "s3cret" {
			return errors.New("secret not passed")
		}
		return nil
	})
	cmd := n.Update(k("t"))
	if cmd == nil || !strings.Contains(ansi.Strip(n.Render(asciiDisplay(false), 100, 30)), "Sending a test message to phone") {
		t.Fatal("t did not start sending")
	}
	n.Update(cmd())
	if out := ansi.Strip(n.Render(asciiDisplay(false), 100, 30)); got != "phone" || !strings.Contains(out, "Test message to phone: sent") {
		t.Errorf("sent to %q:\n%s", got, out)
	}
	n.Update(k("j"))
	n.cursor = 1
	n.send = func(context.Context, config.Notifications, config.Channel, config.Secrets, string) error {
		return errors.New("401 Unauthorized")
	}
	n.Update(n.Update(k("t"))())
	if out := ansi.Strip(n.Render(asciiDisplay(false), 100, 30)); !strings.Contains(out, "Test message to chat: failed: 401 Unauthorized") {
		t.Errorf("failure:\n%s", out)
	}
}

func TestConfigTabs(t *testing.T) {
	s, _ := newSettings(t, "")
	c := configOf(s)
	if out := ansi.Strip(c.Render(asciiDisplay(false), 100, 30)); !strings.Contains(out, "General") || !strings.Contains(out, "enter opens one") {
		t.Fatalf("general:\n%s", out)
	}
	c.Update(k("l"))
	if out := ansi.Strip(c.Render(asciiDisplay(false), 100, 30)); c.tab != tabNotifications || !strings.Contains(out, "No channels") {
		t.Errorf("notifications:\n%s", out)
	}
	c.Update(k("h"))
	if c.tab != tabGeneral {
		t.Error("h did not go back")
	}
}

// The setup box says whether anything sends, and s offers to start fstui
// watch only when nothing does.
func TestNotificationsDelivery(t *testing.T) {
	n := notificationsTab(t, nil)
	for _, c := range []struct {
		jobs     []schedule.Job
		want     string
		offersUp bool
	}{
		{nil, "[ ] Sending: nothing sends these yet", true},
		{[]schedule.Job{{ID: schedule.WatchID, When: schedule.On, By: "systemd unit fstui-watch.service"}}, "[x] Sending: fstui watch runs as a service (systemd unit fstui-watch.service).", false},
		{[]schedule.Job{{ID: schedule.CheckID, When: "every 15m", By: "cron"}, {ID: schedule.WatchID, When: schedule.Off}}, "[x] Sending: fstui check runs every 15m (cron).", false},
	} {
		n.sched = &Schedules{read: func(context.Context) ([]schedule.Job, error) { return nil, nil }, loaded: true, jobs: c.jobs}
		out := ansi.Strip(n.Render(asciiDisplay(false), 110, 40))
		if !strings.Contains(out, c.want) {
			t.Errorf("lacks %q:\n%s", c.want, out)
		}
		offers := false
		for _, b := range n.Keys() {
			offers = offers || b.Help().Desc == keys.Get(keys.Delivery).Help().Desc
		}
		if offers != c.offersUp {
			t.Errorf("%q: offers to set up sending: %v", c.want, offers)
		}
	}
}

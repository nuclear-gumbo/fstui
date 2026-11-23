// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

func alertsTab(t *testing.T) (*Alerts, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("notifications:\n  channels: [{name: phone, type: ntfy, url: \"https://n\", topic: z}, {name: mail, type: webhook, url: \"https://w\"}]\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, problems := config.Parse(file, data)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return NewAlerts(file, cfg, func() []string { return []string{"tank", "vault"} }), file
}

var space = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}

// to puts the cursor on the row of the kind of alert named.
func to(t *testing.T, a *Alerts, name string) {
	t.Helper()
	i := slices.IndexFunc(a.rows(), func(r alertRow) bool { return r.t.Name == name })
	if i < 0 {
		t.Fatalf("no row %s", name)
	}
	a.cursor = i
}

// field moves the open form to the field labelled label.
func field(t *testing.T, a *Alerts, label string) formField {
	t.Helper()
	for i, f := range a.form.fields() {
		if f.label == label {
			a.form.cursor, a.form.sub = i, 0
			return f
		}
	}
	t.Fatalf("no field %q", label)
	return formField{}
}

// fill types s into the field labelled label.
func fill(t *testing.T, a *Alerts, label, s string) {
	t.Helper()
	f := field(t, a, label)
	for range []rune(a.form.values[f.key]) {
		a.Update(k("backspace"))
	}
	for _, r := range s {
		a.Update(k(string(r)))
	}
}

// written writes the reviewed file and reads it back.
func written(t *testing.T, a *Alerts, file string) config.Config {
	t.Helper()
	if a.review == nil {
		problem := ""
		if a.form != nil {
			problem = a.form.problem
		}
		t.Fatalf("no review; message %q, form problem %q", a.message, problem)
	}
	if cmd := a.Update(k("w")); cmd == nil {
		t.Fatalf("w wrote nothing: %s %v", a.message, a.review.problems)
	}
	view(a, 110) // drawn before the saved settings come back, as in fstui
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	c, problems := config.Parse(file, data)
	if len(problems) > 0 {
		t.Fatalf("%v\n%s", problems, data)
	}
	a.Update(ConfigMsg{Config: c})
	return c
}

func view(a *Alerts, w int) string { return ansi.Strip(a.Render(asciiDisplay(false), w, 60)) }

// The list shows every kind of alert by group, ticked when on, with its
// threshold in words; the defaults say they are the defaults.
func TestAlertsList(t *testing.T) {
	a, _ := alertsTab(t)
	out := view(a, 110)
	for _, want := range []string{
		"These are the defaults", "Pool health", "[x] A pool is not ONLINE", "[ ] A device is offline",
		"Space", "[x] A pool is filling up", "from 80%, error from 90%", "all pools", "Your own", "None yet",
		"Sends, for example: tank is DEGRADED: 1 device OFFLINE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

// space turns a kind of alert on for every pool and channel; saving
// writes the list, the defaults with it.
func TestAlertToggle(t *testing.T) {
	a, file := alertsTab(t)
	to(t, a, "device-down")
	a.Update(space)
	to(t, a, "fragmentation")
	a.Update(space)
	a.Update(k("p"))
	c := written(t, a, file)
	var names []string
	for _, al := range c.Notifications.Alerts {
		names = append(names, al.Template)
	}
	if !slices.Contains(names, "device-down") || slices.Contains(names, "fragmentation") || !slices.Contains(names, "pool-unhealthy") {
		t.Errorf("alerts %v", names)
	}
	if strings.Contains(view(a, 110), "These are the defaults") {
		t.Error("still says defaults after saving")
	}
}

// The form picks pools and channels from checklists and keeps only what
// differs from the kind of alert.
func TestAlertForm(t *testing.T) {
	a, file := alertsTab(t)
	to(t, a, "capacity")
	a.Update(k("enter"))
	field(t, a, "Pools")
	a.Update(k("down")) // vault
	a.Update(space)
	if got := a.form.values["pools"]; got != "vault" {
		t.Fatalf("pools %q", got)
	}
	field(t, a, "Channels")
	a.Update(space) // phone
	fill(t, a, "Threshold", "70")
	field(t, a, "How often")
	a.Update(k("e")) // every hour
	if got := a.form.values["repeat"]; got != "1h" {
		t.Fatalf("how often %q", got)
	}
	a.Update(k("ctrl+enter"))
	c := written(t, a, file)
	i := slices.IndexFunc(c.Notifications.Alerts, func(al config.Alert) bool { return al.Template == "capacity" })
	al := c.Notifications.Alerts[i]
	if !slices.Equal(al.Pools, []string{"vault"}) || !slices.Equal(al.Channels, []string{"phone"}) || al.Value != "70" || al.ErrorValue != "" || al.Repeat == nil || al.Text != "" {
		t.Errorf("alert %+v", al)
	}
	if !strings.Contains(view(a, 120), "vault → phone") {
		t.Errorf("scope not shown:\n%s", view(a, 120))
	}
}

// A mistake keeps the form open and says what is wrong.
func TestAlertFormProblem(t *testing.T) {
	a, _ := alertsTab(t)
	to(t, a, "capacity")
	a.Update(k("enter"))
	fill(t, a, "Threshold", "lots")
	a.Update(k("ctrl+enter"))
	if a.form == nil || !strings.Contains(a.form.problem, "value: want a number") {
		t.Fatalf("form %v", a.form)
	}
	if !strings.Contains(view(a, 110), "Cannot finish: value") {
		t.Error("the problem is not shown")
	}
	a.Update(k("esc"))
	if a.form != nil || a.book.edit != nil {
		t.Error("esc kept the form or an edit")
	}
}

// Your own alert: what it watches and where it goes in one form; listed
// under Your own; removed with d twice.
func TestOwnAlert(t *testing.T) {
	a, file := alertsTab(t)
	a.Update(k("a"))
	fill(t, a, "Description", "Backup is nearly full")
	fill(t, a, "Short name", "backup-full")
	field(t, a, "Measure")
	a.Update(k("f")) // free space
	if got := a.form.values["metric"]; got != "free" {
		t.Fatalf("measure %q", got)
	}
	field(t, a, "Sent when it is")
	a.Update(k("b")) // below
	fill(t, a, "Threshold", "1T")
	fill(t, a, "Message", "{pool} has {value} left")
	a.Update(k("ctrl+enter"))
	c := written(t, a, file)
	if len(c.Notifications.Templates) != 1 || c.Notifications.Templates[0].Op != "<" || !slices.ContainsFunc(c.Notifications.Alerts, func(al config.Alert) bool { return al.Template == "backup-full" }) {
		t.Fatalf("templates %+v alerts %+v", c.Notifications.Templates, c.Notifications.Alerts)
	}
	out := view(a, 110)
	if !strings.Contains(out, "[x] Backup is nearly full") || !strings.Contains(out, "below 1.0 TiB") {
		t.Errorf("own alert not listed:\n%s", out)
	}
	to(t, a, "backup-full")
	a.Update(k("d"))
	a.Update(k("d"))
	a.Update(k("p"))
	c = written(t, a, file)
	if len(c.Notifications.Templates) != 0 || slices.ContainsFunc(c.Notifications.Alerts, func(al config.Alert) bool { return al.Template == "backup-full" }) {
		t.Errorf("not removed: %+v", c.Notifications)
	}
}

// esc from the review keeps the file and the edits.
func TestAlertReviewCancel(t *testing.T) {
	a, file := alertsTab(t)
	a.Update(space)
	a.Update(k("p"))
	if a.review == nil {
		t.Fatal("no review")
	}
	a.Update(k("esc"))
	if a.review != nil || a.book.edit == nil {
		t.Error("esc lost the edits or kept the review")
	}
	if data, _ := os.ReadFile(file); strings.Contains(string(data), "alerts") {
		t.Error("the file was written")
	}
}

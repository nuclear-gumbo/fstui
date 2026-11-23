// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// hostsTab is the tab over a real config.yaml in a temporary directory,
// loaded as fstui loads it.
func hostsTab(t *testing.T, yaml string, lister *HostLister) (*Hosts, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir()) // where the review stages the file
	cfg, problems := config.Parse(file, []byte(yaml))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	return NewHosts(file, cfg, lister), file
}

func typeHost(t *testing.T, h *Hosts, label, s string) {
	t.Helper()
	for i, f := range h.form.fields() {
		if f.label == label {
			h.form.cursor = i
			for range []rune(h.form.values[f.key]) {
				h.Update(k("backspace"))
			}
			for _, r := range s {
				h.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			return
		}
	}
	t.Fatalf("no field %q", label)
}

// write saves the review and reads the file back.
func write(t *testing.T, h *Hosts, file string) config.Config {
	t.Helper()
	if h.review == nil {
		t.Fatalf("no review: %s", h.message)
	}
	if cmd := h.Update(k("w")); cmd == nil {
		t.Fatalf("w wrote nothing: %s", h.message)
	}
	h.Render(asciiDisplay(false), 100, 30) // drawn before the saved settings come back, as in fstui
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	c, problems := config.Parse(file, b)
	if len(problems) > 0 {
		t.Fatalf("%v\n%s", problems, b)
	}
	return c
}

func TestHostsTabAdd(t *testing.T) {
	h, file := hostsTab(t, "theme: nord\n", nil)
	if out := ansi.Strip(h.Render(asciiDisplay(false), 100, 30)); !strings.Contains(out, "[x]     local  this machine") || !strings.Contains(out, "No other hosts yet") {
		t.Fatalf("empty tab:\n%s", out)
	}
	h.Update(k("a"))
	typeHost(t, h, "Name", "pve1")
	typeHost(t, h, "Destination", "root@pve1")
	h.Update(k("ctrl+enter"))
	if out := ansi.Strip(h.Render(asciiDisplay(false), 100, 30)); !strings.Contains(out, "+ hosts:") || !strings.Contains(out, "root@pve1") {
		t.Fatalf("review:\n%s", out)
	}
	c := write(t, h, file)
	if len(c.Hosts) != 1 || c.Hosts[0] != (config.Host{Name: "pve1", Destination: "root@pve1"}) || c.Theme != "nord" {
		t.Errorf("file: %+v", c.Hosts)
	}
}

// A destination ssh could read as an option is refused in the review,
// and nothing is written.
func TestHostsTabRefusesBadDestination(t *testing.T) {
	h, file := hostsTab(t, "", nil)
	h.Update(k("a"))
	typeHost(t, h, "Name", "evil")
	typeHost(t, h, "Destination", "-oProxyCommand=sh")
	h.Update(k("ctrl+enter"))
	out := ansi.Strip(h.Render(asciiDisplay(false), 100, 30))
	if !strings.Contains(out, "Cannot save") {
		t.Fatalf("review:\n%s", out)
	}
	h.Update(k("w"))
	if b, _ := os.ReadFile(file); len(b) != 0 {
		t.Errorf("written: %s", b)
	}
}

// space checks a host with fstui check; the last checked machine stays
// checked; removing a host unchecks it, renaming keeps it checked.
func TestHostsTabCheck(t *testing.T) {
	h, file := hostsTab(t, "hosts:\n  - {name: pve1, destination: pve1}\n  - {name: nas, destination: nas}\n", nil)
	h.Update(k(" "))
	if !strings.Contains(h.message, "needs a machine") { // the last one stays ticked
		t.Errorf("unchecked the last machine: %q", h.message)
	}
	h.Update(k("j"))
	h.Update(k(" "))
	h.Update(k("p"))
	c := write(t, h, file)
	if strings.Join(c.Notifications.Hosts, ",") != "local,pve1" {
		t.Fatalf("checked %v", c.Notifications.Hosts)
	}
	h.Update(ConfigMsg{Config: c})

	h.Update(k("enter"))
	typeHost(t, h, "Name", "pve9")
	h.Update(k("ctrl+enter"))
	c = write(t, h, file)
	if strings.Join(c.Notifications.Hosts, ",") != "local,pve9" || c.Hosts[0].Name != "pve9" {
		t.Fatalf("after rename: %v %v", c.Hosts, c.Notifications.Hosts)
	}
	h.Update(ConfigMsg{Config: c})

	h.Update(k("d"))
	h.Update(k("d"))
	h.Update(k("p"))
	c = write(t, h, file)
	if len(c.Hosts) != 1 || strings.Join(c.Notifications.Hosts, ",") != "local" {
		t.Errorf("after remove: %v %v", c.Hosts, c.Notifications.Hosts)
	}
}

// t reads the selected host as the host list does.
func TestHostsTabTest(t *testing.T) {
	var asked HostEntry
	lister := &HostLister{
		Entries: func(config.Config) []HostEntry { return nil },
		Status: func(_ context.Context, _ config.Config, e HostEntry) (string, []HostEntry) {
			asked = e
			return "2 pools", nil
		},
	}
	h, _ := hostsTab(t, "hosts:\n  - {name: pve1, destination: root@pve1}\n", lister)
	h.Update(k("j"))
	cmd := h.Update(k("t"))
	if cmd == nil {
		t.Fatal("t did nothing")
	}
	h.Update(cmd())
	if asked.Destination != "root@pve1" || !strings.Contains(ansi.Strip(h.Render(asciiDisplay(false), 100, 30)), "pve1: 2 pools") {
		t.Errorf("asked %+v:\n%s", asked, ansi.Strip(h.Render(asciiDisplay(false), 100, 30)))
	}
}

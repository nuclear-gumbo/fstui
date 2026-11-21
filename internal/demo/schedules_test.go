// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_nowrite

package demo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// The demo's system takes schedule steps as systemd would, and only
// inside its own directory.
func TestDemoSchedules(t *testing.T) {
	s, err := NewSchedules()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	wants := filepath.Join(s.Root, "etc/systemd/system/timers.target.wants")
	steps := []execute.Argv{
		{Tool: execute.Systemctl, Options: []string{"enable", "--now"}, Operands: []string{"zfs-scrub-weekly@tank.timer"}},
		{Tool: execute.Systemctl, Options: []string{"disable", "--now"}, Operands: []string{"zfs-trim-weekly@fast.timer"}},
		{Tool: execute.Tee, Operands: []string{"/etc/systemd/system/.fstui-check.timer.fstui-new"}},
		{Tool: execute.Mv, Operands: []string{"/etc/systemd/system/.fstui-check.timer.fstui-new", "/etc/systemd/system/fstui-check.timer"}},
	}
	for _, a := range steps {
		if !s.Handles(a) {
			t.Fatalf("%s is not handled", a)
		}
		if _, err := s.Apply(a, []byte("[Timer]\n")); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(wants, "zfs-scrub-weekly@tank.timer")); err != nil {
		t.Error("enable made no link")
	}
	if _, err := os.Lstat(filepath.Join(wants, "zfs-trim-weekly@fast.timer")); err == nil {
		t.Error("disable left the link")
	}
	if b, err := os.ReadFile(filepath.Join(s.Root, "etc/systemd/system/fstui-check.timer")); err != nil || string(b) != "[Timer]\n" {
		t.Errorf("unit file: %q %v", b, err)
	}
	if s.Handles(execute.Argv{Tool: execute.Tee, Operands: []string{"/sys/module/zfs/parameters/zfs_arc_max"}}) {
		t.Error("a module parameter is taken as a schedule step")
	}
	res, err := s.Read(t.Context(), execute.Argv{Tool: execute.Systemctl, Options: []string{"list-timers"}})
	if err != nil || !strings.Contains(string(res.Stdout), "zfs-scrub-weekly@tank.timer") {
		t.Errorf("list-timers: %s %v", res.Stdout, err)
	}
}

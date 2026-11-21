// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An alert may name a channel another file lists, and the check of an
// edited file reports only what the edit got wrong.
func TestCheckAmong(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "config.yaml")
	dropIn := filepath.Join(dir, DropInDir, "channels.yaml")
	if err := os.MkdirAll(filepath.Dir(dropIn), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropIn, []byte("notifications:\n  channels: [{name: phone, type: ntfy, url: \"https://n\", topic: z}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := trust{euid: uint32(os.Geteuid()), top: dir} //nolint:gosec // uids are non-negative
	files := []string{user, dropIn}
	ok := []byte("notifications:\n  alerts: [{template: capacity, channels: [phone]}]\n")
	if p := tr.checkAmong(files, user, ok); len(p) > 0 {
		t.Errorf("a channel from the drop-in: %v", p)
	}
	if p := Check(user, ok); len(p) == 0 {
		t.Error("alone, the file should not know the channel")
	}
	bad := []byte("notifications:\n  alerts: [{template: capacity, channels: [ghost]}]\n")
	if p := tr.checkAmong(files, user, bad); len(p) != 1 || !strings.Contains(p[0].Msg, `no channel named "ghost"`) {
		t.Errorf("unknown channel: %v", p)
	}
}

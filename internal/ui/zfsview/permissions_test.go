// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func capturedAllow(t *testing.T, file string) []zfs.Delegation {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", "allow", file))
	if err != nil {
		t.Fatal(err)
	}
	d, err := zfs.ParseAllow(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func permText(ds string, d []zfs.Delegation) string {
	return ansi.Strip(strings.Join(permissionLines(display(), ds, d, 120), "\n"))
}

// From the capture (see zfs.TestParseAllow): seen from home/sub, its own
// grant applies here; home's grants made for descendents reach it, its
// local grant to group users does not.
func TestPermissionsSeenFromBelow(t *testing.T) {
	out := permText("fstuitest_allow/home/sub", capturedAllow(t, "nested.txt"))
	for _, want := range []string{
		"user nobody", "userprop", "here",
		"user dev", "create,mount,snapshot", "here, from fstuitest_allow/home",
		"everyone", "@backup",
		"dataset creator", "on what they create below fstuitest_allow/home",
		"@backup", "hold,send,snapshot", "set defined on fstuitest_allow",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "group users") {
		t.Errorf("home's local grant shown on sub:\n%s", out)
	}
}

// Seen from home itself, every grant set there shows with its scope.
func TestPermissionsSeenOnTheDataset(t *testing.T) {
	out := permText("fstuitest_allow/home", capturedAllow(t, "nested.txt")[1:])
	for _, want := range []string{"group users", "here", "on datasets below only", "here and below"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestPermissionsNone(t *testing.T) {
	if out := permText("tank", nil); !strings.Contains(out, "Nothing is delegated") || !strings.Contains(out, "Only root") {
		t.Errorf("none:\n%s", out)
	}
	if out := permText("fstuitest_allow/plain", capturedAllow(t, "inherited-sets-only.txt")); !strings.Contains(out, "No one has delegated") || !strings.Contains(out, "@backup") {
		t.Errorf("sets only:\n%s", out)
	}
}

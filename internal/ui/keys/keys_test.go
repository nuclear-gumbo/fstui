// SPDX-License-Identifier: GPL-3.0-or-later

package keys

import (
	"slices"
	"strings"
	"testing"
)

// The defaults follow vim: h/l for tabs and tree nodes, j/k to move, and both
// enter and space to open.
func TestVimDefaults(t *testing.T) {
	Reset()
	for a, want := range map[Action][]string{
		Left: {"h", "left"}, Right: {"l", "right"}, Up: {"k", "up"}, Down: {"j", "down"},
		Select: {"enter", "space"}, First: {"g", "home"}, Last: {"G", "end"},
	} {
		if got := Get(a).Keys(); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", a, got, want)
		}
	}
}

func TestConfigure(t *testing.T) {
	t.Cleanup(Reset)
	problems := Configure(map[string][]string{
		"select":  {"o"},
		"refresh": {"j"}, // also Down's default
		"jump":    {"J"},
		"up":      {},
	})
	got := strings.Join(problems, "\n")
	for _, want := range []string{`unknown action "jump"`, "keys.up: no keys given", `keys.refresh: "j" is also bound to down`} {
		if !strings.Contains(got, want) {
			t.Errorf("problems lack %q:\n%s", want, got)
		}
	}
	if !slices.Equal(Get(Select).Keys(), []string{"o"}) || !slices.Equal(Get(Up).Keys(), []string{"k", "up"}) {
		t.Errorf("select %v, up %v", Get(Select).Keys(), Get(Up).Keys())
	}
	if b := As(Select, "open pool"); b.Help().Desc != "open pool" || b.Help().Key != "o" {
		t.Errorf("As = %+v", b.Help())
	}
}

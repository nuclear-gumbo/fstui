// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"slices"
	"strings"
	"testing"
)

func TestEffective(t *testing.T) {
	out := `---- Permissions on tank/home/alice ----------------------------------
Local permissions:
	user alice destroy
---- Permissions on tank/home ----------------------------------------
Permission sets:
	@snaps hold,release,snapshot
Local+Descendent permissions:
	group staff @snaps,mount
Descendent permissions:
	everyone rename
Local permissions:
	user alice create
`
	dels, err := ParseAllow([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	got := Effective(dels, "tank/home/alice", "alice", []string{"users", "staff"})
	var names []string
	for p := range got {
		names = append(names, p)
	}
	slices.Sort(names)
	// create was granted on tank/home with local scope only: not here.
	if want := "destroy hold mount release rename snapshot"; strings.Join(names, " ") != want {
		t.Fatalf("Effective = %v, want %s", names, want)
	}
	if got := Effective(dels, "tank/home/alice", "bob", nil); !got["rename"] || got["destroy"] || got["snapshot"] {
		t.Fatalf("bob: %v", got)
	}
}

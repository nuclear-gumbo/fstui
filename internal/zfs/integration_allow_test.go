// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Delegations made with zfs allow read back with their scope, and a
// child sees its parent's block after its own.
func TestIntegrationAllow(t *testing.T) {
	tp := testpool.New(t, "", 1)
	tp.CreateFilesystem("home")
	tp.CreateFilesystem("home/sub")
	tp.Delegate("home", "-d", "-u", "nobody", "snapshot,mount")
	tp.Delegate("home/sub", "-l", "-e", "userprop")

	ds, err := name.ParseDataset(tp.Name + "/home/sub")
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewReader(runner(t)).Allow(t.Context(), ds)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Dataset.Raw() != ds.String() || got[1].Dataset.Raw() != tp.Name+"/home" {
		t.Fatalf("blocks %+v", got)
	}
	sub, home := got[0].Grants, got[1].Grants
	if len(sub) != 1 || sub[0].Scope != Local || sub[0].Who != "everyone" || strings.Join(sub[0].Perms, ",") != "userprop" {
		t.Errorf("sub %+v", sub)
	}
	if len(home) != 1 || home[0].Scope != Descendent || home[0].Name.Raw() != "nobody" || strings.Join(home[0].Perms, ",") != "mount,snapshot" {
		t.Errorf("home %+v", home)
	}
}

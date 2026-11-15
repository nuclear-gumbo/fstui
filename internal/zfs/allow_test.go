// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"strings"
	"testing"
)

// The capture: fstuitest_allow defines @backup; home grants dev
// create, mount and snapshot here and below, group users destroy here
// only, everyone @backup below only, and destroy at create time; sub
// grants nobody userprop here only.
func TestParseAllow(t *testing.T) {
	ds, err := ParseAllow(golden(t, "allow", "nested.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 || ds[0].Dataset.Raw() != "fstuitest_allow/home/sub" || ds[2].Dataset.Raw() != "fstuitest_allow" {
		t.Fatalf("blocks %+v", ds)
	}
	sub, home, pool := ds[0], ds[1], ds[2]
	if len(sub.Grants) != 1 || sub.Grants[0].Scope != Local || sub.Grants[0].Who != "user" ||
		sub.Grants[0].Name.Raw() != "nobody" || strings.Join(sub.Grants[0].Perms, ",") != "userprop" {
		t.Errorf("sub %+v", sub)
	}
	if strings.Join(home.CreateTime, ",") != "destroy" || len(home.Grants) != 3 {
		t.Fatalf("home %+v", home)
	}
	want := []struct {
		scope GrantScope
		who   string
		perms string
	}{{Local, "group", "destroy"}, {Descendent, "everyone", "@backup"}, {LocalAndDescendent, "user", "create,mount,snapshot"}}
	for i, w := range want {
		g := home.Grants[i]
		if g.Scope != w.scope || g.Who != w.who || strings.Join(g.Perms, ",") != w.perms {
			t.Errorf("home grant %d: %+v", i, g)
		}
	}
	if len(pool.Sets) != 1 || pool.Sets[0].Name != "@backup" || strings.Join(pool.Sets[0].Perms, ",") != "hold,send,snapshot" {
		t.Errorf("pool sets %+v", pool.Sets)
	}
}

func TestParseAllowEmptyAndErrors(t *testing.T) {
	if ds, err := ParseAllow(golden(t, "allow", "none.txt")); err != nil || len(ds) != 0 {
		t.Errorf("nothing delegated: %v, %v", ds, err)
	}
	if ds, err := ParseAllow(golden(t, "allow", "inherited-sets-only.txt")); err != nil || len(ds) != 1 || len(ds[0].Sets) != 1 {
		t.Errorf("sets only: %+v, %v", ds, err)
	}
	if _, err := ParseAllow([]byte(blockPrefix + "tank ---\nLocal permissions:\n\tuser\n")); err == nil {
		t.Error("an entry without permissions was accepted")
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import "testing"

func TestParseDestroyDryRun(t *testing.T) {
	p, err := ParseDestroyDryRun([]byte("destroy\ttank/a@1\ndestroy\ttank/a/b@1\nreclaim\t4096\n"))
	if err != nil || len(p.Destroys) != 2 || p.Destroys[1] != "tank/a/b@1" || p.Reclaim != 4096 {
		t.Fatalf("parse = %+v, %v", p, err)
	}
	for _, bad := range []string{"destroy tank/a@1\n", "reclaim\tlots\n", "frobnicate\tx\n"} {
		if _, err := ParseDestroyDryRun([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

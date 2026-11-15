// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import "testing"

// Recorded from zfs diff -H -F -t on 2.3.9, paths shortened.
const diffOut = "1794719291.626864977\tM\tF\t/mnt/keep\n" +
	"1794719291.558864141\t-\tF\t/mnt/gone\n" +
	"1794719291.634865076\tR\tF\t/mnt/old\t/mnt/new\n" +
	"1794719291.638865125\t+\tF\t/mnt/e\\0033[2Jvil\n" +
	"1794719291.638865125\t+\tF\t/mnt/sp\\0040ace\n" +
	"1794719291.642865174\t+\t/\t/mnt/dir/sub\n"

func TestParseDiff(t *testing.T) {
	got, truncated, err := ParseDiff([]byte(diffOut))
	if err != nil || truncated || len(got) != 6 {
		t.Fatalf("ParseDiff = %d entries, %v, %v", len(got), truncated, err)
	}
	if got[2].Change != 'R' || got[2].Path.Raw() != "/mnt/old" || got[2].NewPath.Raw() != "/mnt/new" {
		t.Errorf("rename = %+v", got[2])
	}
	if got[3].Path.Raw() != "/mnt/e\x1b[2Jvil" || got[4].Path.Raw() != "/mnt/sp ace" || got[5].Type != '/' {
		t.Errorf("escapes = %q, %q, type %c", got[3].Path.Raw(), got[4].Path.Raw(), got[5].Type)
	}
	if got[0].Time.Unix() != 1794719291 {
		t.Errorf("time = %v", got[0].Time)
	}
	if _, _, err := ParseDiff([]byte("garbage\n")); err == nil {
		t.Error("garbage parsed")
	}
}

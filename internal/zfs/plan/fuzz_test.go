// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strconv"
	"strings"
	"testing"
)

// ParseSize never panics, and for a plain decimal number it agrees with
// strconv: no wrap-around on overflow.
func FuzzParseSize(f *testing.F) {
	for _, s := range []string{"512", "10G", "4KiB", "16E", "18446744073709551615", "18446744073709551616", "99999999999P", "-1", "1.5G"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := ParseSize(s)
		if strings.Trim(s, "0123456789") != "" || s == "" {
			return
		}
		want, werr := strconv.ParseUint(s, 10, 64)
		if (err == nil) != (werr == nil) || (err == nil && n != want) {
			t.Errorf("ParseSize(%q) = %d, %v; strconv says %d, %v", s, n, err, want, werr)
		}
	})
}

// modprobeEdit keeps every line that is not an "options zfs" line, and
// editing its own output again with the same limits changes nothing.
func FuzzModprobeEdit(f *testing.F) {
	f.Add("# mine\noptions zfs zfs_arc_max=1 zfs_prefetch_disable=1\n", uint64(2147483648), uint64(0))
	f.Add("install zfs /bin/true\noptions zfs\n", uint64(0), uint64(1<<30))
	f.Fuzz(func(t *testing.T, old string, maxV, minV uint64) {
		set := map[string]*uint64{"zfs_arc_max": &maxV, "zfs_arc_min": &minV}
		once := modprobeEdit(old, set)
		if twice := modprobeEdit(once, set); twice != once {
			t.Errorf("not stable:\n%q\n%q", once, twice)
		}
		for _, l := range strings.Split(old, "\n") {
			f := strings.Fields(l)
			zfsOptions := len(f) >= 2 && f[0] == "options" && f[1] == "zfs"
			if !zfsOptions && strings.TrimSpace(l) != "" && !strings.Contains(once, l) {
				t.Errorf("line %q lost", l)
			}
		}
	})
}

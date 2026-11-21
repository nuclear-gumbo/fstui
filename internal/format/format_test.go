// SPDX-License-Identifier: GPL-3.0-or-later

package format

import (
	"testing"
	"time"
)

func TestSize(t *testing.T) {
	for _, c := range []struct {
		n     uint64
		exact bool
		want  string
	}{
		{0, false, "0 B"},
		{1023, false, "1023 B"},
		{1536, false, "1.5 KiB"},
		{1023<<20 + 900<<10, false, "1.0 GiB"},
		{200 << 30, false, "200 GiB"},
		{1234567, true, "1 234 567"},
		{12, true, "12"},
	} {
		if got := Size(c.n, c.exact); got != c.want {
			t.Errorf("Size(%d, %v) = %q, want %q", c.n, c.exact, got, c.want)
		}
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second: "0s", 3 * time.Second: "3s", 14 * time.Minute: "14m",
		2 * time.Hour: "2h", 5 * 24 * time.Hour: "5d",
	} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %s, want %s", d, got, want)
		}
	}
}

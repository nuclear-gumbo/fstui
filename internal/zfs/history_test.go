// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"strings"
	"testing"
	"time"
)

func TestParseHistory(t *testing.T) {
	es, err := ParseHistory(golden(t, "healthy", "zpool-history.txt"))
	if err != nil {
		t.Fatal(err)
	}
	first := es[0]
	if !first.Internal || first.TXG != 4 || !strings.HasPrefix(first.Text.Raw(), "create pool version 5000") {
		t.Errorf("first entry = %+v, want the internal create at txg 4", first)
	}
	if want := time.Date(2026, 11, 12, 23, 41, 6, 0, time.Local); !first.Time.Equal(want) {
		t.Errorf("time = %v, want %v", first.Time, want)
	}
	var commands, ioctls int
	for _, e := range es {
		switch {
		case !e.Internal:
			commands++
			if !strings.HasPrefix(e.Text.Raw(), "zpool ") && !strings.HasPrefix(e.Text.Raw(), "zfs ") {
				t.Errorf("user entry is not a command: %q", e.Text.Raw())
			}
		case strings.Contains(e.Text.Raw(), ") ioctl "):
			ioctls++
			if !strings.Contains(e.Text.Raw(), "\n") {
				t.Errorf("ioctl record lost its argument lines: %q", e.Text.Raw())
			}
		}
	}
	// capture-golden.sh's healthy scenario: zpool create, five zfs create,
	// two snapshots, two holds, bookmark, clone, set, unload-key = 14.
	// zfs unmount is not logged.
	if commands != 14 || ioctls == 0 {
		t.Errorf("%d commands and %d ioctl records", commands, ioctls)
	}
}

// The capture sets a user property containing a screen clear, a title
// sequence, BEL and CR; zpool history prints it raw.
func TestHistoryHostileTextStaysVisible(t *testing.T) {
	es, err := ParseHistory(golden(t, "healthy", "zpool-history.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if !strings.Contains(e.Text.Raw(), "com.example:note=evil") {
			continue
		}
		if d := e.Text.Display(); strings.ContainsAny(d, "\x1b\x07\r") || !strings.Contains(d, `evil\x1b[2J`) {
			t.Errorf("display = %q", d)
		}
		return
	}
	t.Fatal("the hostile zfs set is not in the history")
}

func TestParseHistoryContinuationAndErrors(t *testing.T) {
	in := "History for 'p':\n2026-01-02.03:04:05 zfs set com.example:x=line1\nline2 p\n"
	es, err := ParseHistory([]byte(in))
	if err != nil || len(es) != 1 || es[0].Text.Raw() != "zfs set com.example:x=line1\nline2 p" {
		t.Fatalf("continuation: %+v, %v", es, err)
	}
	for _, bad := range []string{"", "no header\n", "History for 'p':\nstray text\n"} {
		if _, err := ParseHistory([]byte(bad)); err == nil {
			t.Errorf("%q parsed without error", bad)
		}
	}
}

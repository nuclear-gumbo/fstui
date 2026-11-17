// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseHistory(t *testing.T) {
	h, err := ParseHistory(golden(t, "healthy", "zpool-history.txt"))
	if err != nil {
		t.Fatal(err)
	}
	es := append(append([]HistoryEntry(nil), h.User...), h.Internal...)
	if h.UserTotal != len(h.User) || h.InternalTotal != len(h.Internal) {
		t.Errorf("totals %d %d for %d %d kept", h.UserTotal, h.InternalTotal, len(h.User), len(h.Internal))
	}
	first := h.Internal[0]
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
	h, err := ParseHistory(golden(t, "healthy", "zpool-history.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range h.User {
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
	h, err := ParseHistory([]byte(in))
	if err != nil || len(h.User) != 1 || h.User[0].Text.Raw() != "zfs set com.example:x=line1\nline2 p" {
		t.Fatalf("continuation: %+v, %v", h, err)
	}
	for _, bad := range []string{"", "no header\n", "History for 'p':\nstray text\n"} {
		if _, err := ParseHistory([]byte(bad)); err == nil {
			t.Errorf("%q parsed without error", bad)
		}
	}
}

// A long history keeps the most recent entries of each kind; internal
// events never push out the commands people ran.
func TestHistoryKeepsRecent(t *testing.T) {
	p := historyParser{keep: 3}
	lines := []string{"History for 'p':", "2026-01-02.03:04:05 zpool create p sda"}
	for i := range 20 {
		lines = append(lines, fmt.Sprintf("2026-01-02.03:04:%02d [txg:%d] snapshot p@%d (%d) ", i, 10+i, i, i), "    more")
	}
	lines = append(lines, "2026-01-02.03:05:00 zfs snapshot p@last")
	for _, l := range lines {
		if err := p.line(l); err != nil {
			t.Fatal(err)
		}
	}
	h, err := p.result()
	if err != nil || h.UserTotal != 2 || h.InternalTotal != 20 || len(h.User) != 2 || len(h.Internal) != 3 {
		t.Fatalf("%+v, %v", h, err)
	}
	if last := h.Internal[2]; last.TXG != 29 || !strings.HasSuffix(last.Text.Raw(), "\n    more") || last.Seq != 20 {
		t.Errorf("newest internal %+v", last)
	}
	if h.User[1].Seq != 21 {
		t.Errorf("user seq %d", h.User[1].Seq)
	}
}

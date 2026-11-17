// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// sendFor opens the window on snapshots and runs its first estimate.
func sendFor(t *testing.T, src *countingSnapshots, snaps ...zfs.SnapshotRow) *sendWindow {
	t.Helper()
	w := &sendWindow{src: src, marked: snaps}
	w.Update(w.estimate()())
	return w
}

func sendRender(w *sendWindow) string { return ansi.Strip(w.Render(display(), 100, 40)) }

func snapAt(n string, txg uint64) zfs.SnapshotRow {
	return zfs.SnapshotRow{Name: n, CreateTXG: txg}
}

// One chain per dataset: a full stream, then increments; the total is
// their sum, and the commands are the real zfs send commands.
func TestSendWindowEstimatesChains(t *testing.T) {
	src := &countingSnapshots{}
	w := sendFor(t, src, snapAt("tank/a@1", 1), snapAt("tank/a@2", 2), snapAt("tank/b@x", 3))
	out := sendRender(w)
	for _, want := range []string{
		"nothing is sent", "full tank/a@1", "tank/a @1 → @2", "full tank/b@x", "Total 2.1 KiB",
		"zfs send -- tank/a@1", "zfs send -i tank/a@1 -- tank/a@2", "zfs send -- tank/b@x",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	w.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if !w.loading {
		t.Fatal("c did not estimate again")
	}
	w.Update(w.estimate()())
	if out := sendRender(w); !strings.Contains(out, "Total 1.0 KiB") || !strings.Contains(out, "zfs send -c -- tank/a@1") {
		t.Errorf("compressed:\n%s", out)
	}
}

// A dry run succeeds without the key; the send would not. Without raw,
// a dataset whose key is not loaded gets no estimate and no command.
func TestSendWindowNeedsRawWithoutTheKey(t *testing.T) {
	src := &countingSnapshots{locked: map[string]bool{"tank/secret": true}}
	w := sendFor(t, src, snapAt("tank/secret@1", 1))
	out := sendRender(w)
	if !strings.Contains(out, "not loaded; only a raw send (w) works") || strings.Contains(out, "zfs send -- tank/secret@1") {
		t.Errorf("without raw:\n%s", out)
	}
	w.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	w.Update(w.estimate()())
	if out := sendRender(w); !strings.Contains(out, "zfs send -w -- tank/secret@1") {
		t.Errorf("raw:\n%s", out)
	}
}

func TestSendWindowCapacity(t *testing.T) {
	src := &countingSnapshots{}
	w := sendFor(t, src, snapAt("tank/a@1", 1))
	for _, k := range "t1500" {
		w.Update(tea.KeyPressMsg{Code: k, Text: string(k)})
	}
	w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if out := sendRender(w); !strings.Contains(out, "of 1.5 KiB, 66%") {
		t.Errorf("capacity:\n%s", out)
	}
	w.capacity = 500
	if out := sendRender(w); !strings.Contains(out, "does not fit") {
		t.Errorf("over capacity:\n%s", out)
	}
}

func TestParseCapacity(t *testing.T) {
	for in, want := range map[string]uint64{
		"18TB": 18e12, "16T": 16 << 40, "16TiB": 16 << 40, "2.5GB": 2.5e9, "1500": 1500, "1k": 1 << 10,
	} {
		if got, err := parseCapacity(in); err != nil || got != want {
			t.Errorf("parseCapacity(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "TB", "-1T", "1XB", "1e30PB", "x"} {
		if _, err := parseCapacity(bad); err == nil {
			t.Errorf("parseCapacity(%q) accepted", bad)
		}
	}
}

var _ ui.Modal = (*sendWindow)(nil)

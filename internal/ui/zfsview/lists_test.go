// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

type countingSnapshots struct {
	fake   *fakeDatasets
	calls  int
	locked map[string]bool // encrypted datasets whose key is not loaded
}

// EstimateSend gives fixed sizes: 1000 bytes for a full stream, 100 for
// an incremental one, half with compression.
func (c *countingSnapshots) EstimateSend(_ context.Context, s zfs.SendStep, o zfs.SendOptions) (zfs.SendEstimate, error) {
	n := uint64(1000)
	if s.From != "" {
		n = 100
	}
	if o.Compressed {
		n /= 2
	}
	return zfs.SendEstimate{Streams: []zfs.SendStream{{From: s.From, To: s.To, Bytes: n}}, Total: n}, nil
}

func (c *countingSnapshots) Get(_ context.Context, names, _ []string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	for _, n := range names {
		out[n] = map[string]string{"encryption": "off", "keystatus": "-"}
		if c.locked[n] {
			out[n] = map[string]string{"encryption": "aes-256-gcm", "keystatus": "unavailable"}
		}
	}
	return out, nil
}

func (c *countingSnapshots) AllSnapshots(context.Context) ([]zfs.SnapshotRow, error) {
	c.calls++
	var out []zfs.SnapshotRow
	for _, d := range c.fake.all {
		if d.Type == zfs.Snapshot {
			out = append(out, snapRow(d))
		}
	}
	return out, nil
}

func (c *countingSnapshots) AllVolumes(context.Context) ([]zfs.Dataset, error) {
	var out []zfs.Dataset
	for _, d := range c.fake.all {
		if d.Type == zfs.Volume {
			out = append(out, d)
		}
	}
	return out, nil
}

func clock() time.Time { return now }

func TestSnapshotsReadOnlyWhenShown(t *testing.T) {
	src := &countingSnapshots{fake: healthyDatasets(t)}
	v := NewSnapshots(src, clock)
	if v.Init() != nil {
		t.Fatal("the global snapshot list was read at startup")
	}
	cmd := v.Activate()
	_, tick := v.Update(cmd())
	if v.Activate() != nil {
		t.Fatal("a second visit read the list again")
	}
	// The poller schedules a tick after the read; it must not read again.
	if _, again := v.Update(pollDue[allSnapshots]{timer: v.poll.timer}); again != nil || tick == nil {
		t.Fatal("the list was re-read on a timer")
	}
	if _, r := v.Update(kp("r")); r == nil {
		t.Fatal("r did not reload")
	}
	if src.calls != 1 {
		t.Fatalf("%d reads before r ran", src.calls)
	}
	out := ansi.Strip(v.Render(display(), 140, 20))
	for _, want := range []string{"4 snapshots, sorted by created", "fstuitest_g_healthy/home@daily-1", "fstuitest_g_healthy/home/user@daily-2", "fstuitest_g_healthy/clone"} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshots view lacks %q:\n%s", want, out)
		}
	}
}

func TestVolumes(t *testing.T) {
	v := NewVolumes(&countingSnapshots{fake: healthyDatasets(t)}, interval, clock)
	v.Update(v.Init()())
	out := ansi.Strip(v.Render(display(), 140, 10))
	for _, want := range []string{"fstuitest_g_healthy/vol", "64.0 MiB", "16.0 KiB", "sparse", "/dev/zvol/fstuitest_g_healthy/vol"} {
		if !strings.Contains(out, want) {
			t.Errorf("volumes view lacks %q:\n%s", want, out)
		}
	}
	if narrow := ansi.Strip(v.Render(display(), 80, 10)); strings.Contains(narrow, "/dev/zvol") {
		t.Errorf("80 columns show the device column:\n%s", narrow)
	}
}

func (c *countingSnapshots) Diff(context.Context, string, string) ([]zfs.DiffEntry, bool, error) {
	return []zfs.DiffEntry{
		{Change: '+', Type: 'F', Path: termtext.New("/tank/new\x1b[2J")},
		{Change: 'R', Type: 'F', Path: termtext.New("/tank/a"), NewPath: termtext.New("/tank/b")},
	}, false, nil
}

func TestDiffWindowFromSnapshots(t *testing.T) {
	src := &countingSnapshots{fake: healthyDatasets(t)}
	v := NewSnapshots(src, clock)
	v.Update(v.Activate()())
	if _, cmd := v.Update(kp("=")); cmd == nil {
		t.Fatal("= opened nothing")
	}
	w, open, read := diffCmds(src, "tank/home@daily-1")
	if m, ok := open().(ui.OpenModalMsg); !ok || m.Modal != w {
		t.Fatal("the window does not open first")
	}
	w.Update(read())
	if w.loading {
		t.Fatal("the read did not reach the window")
	}
	out := ansi.Strip(w.Render(display(), 100, 20))
	for _, want := range []string{"1 added, 0 removed, 0 modified, 1 renamed", "/tank/a → /tank/b", "changed since"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Error("a raw escape from a file name reached the screen")
	}
}

func TestSnapshotsMarkSeveral(t *testing.T) {
	src := &countingSnapshots{fake: healthyDatasets(t)}
	v := NewSnapshots(src, clock)
	v.Update(v.Activate()())
	if len(v.poll.data) < 2 {
		t.Skip("the fixture has fewer than two snapshots")
	}
	v.Update(kp("m"))
	v.Update(kp("m"))
	sel, ok := v.Selection()
	if !ok || len(sel.Names) != 2 || sel.Kind != "snapshot" {
		t.Fatalf("selection with two marks: %+v", sel)
	}
	if out := ansi.Strip(v.Render(display(), 100, 20)); strings.Count(out, "*") < 2 {
		t.Errorf("marks not shown:\n%s", out)
	}
	v.Update(kp("M"))
	if sel, _ := v.Selection(); len(sel.Names) != 1 {
		t.Fatalf("after M: %+v", sel)
	}
	v.Update(kp("m"))
	v.Update(ui.ChangedMsg{})
	if len(v.marked) != 0 {
		t.Error("marks kept after a change ran")
	}
}

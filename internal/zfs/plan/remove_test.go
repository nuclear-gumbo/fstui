// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// twoMirrors is tank with mirror-0 (x1, x2) and mirror-1 (y1, y2), each
// 4 TiB with 1 TiB used, and a log device.
func twoMirrors() *fakeReader {
	leaf := func(n string) zfs.Vdev {
		return zfs.Vdev{Name: termtext.New(n), Type: "disk", Path: n, State: "ONLINE"}
	}
	mirror := func(n string, a, b string) zfs.Vdev {
		return zfs.Vdev{Name: termtext.New(n), Type: "mirror", State: "ONLINE", Size: 4 * tib, Alloc: tib, Children: []zfs.Vdev{leaf(a), leaf(b)}}
	}
	st := zfs.PoolStatus{
		GUID: 7, Data: zfs.Vdev{Type: "root", Children: []zfs.Vdev{mirror("mirror-0", "/dev/x1", "/dev/x2"), mirror("mirror-1", "/dev/y1", "/dev/y2")}},
		Logs: []zfs.Vdev{leaf("/dev/log1")},
	}
	return &fakeReader{topo: map[string]zfs.PoolStatus{"tank": st}}
}

func TestVdevRemove(t *testing.T) {
	p, err := Builder{R: twoMirrors()}.VdevRemove(t.Context(), "tank", "/dev/y2")
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if p.Steps[0].String() != "zpool remove -- tank mirror-1" || mutate.TierOf(p) != mutate.High || p.Preview.Output.IsEmpty() || len(p.Irreversible) != 1 {
		t.Errorf("steps %v, tier %v, dry run %q, irreversible %v", p.Steps, mutate.TierOf(p), p.Preview.Output.Raw(), p.Irreversible)
	}
	p, _ = Builder{R: twoMirrors()}.VdevRemove(t.Context(), "tank", "/dev/log1")
	if len(p.Blocked) != 0 || p.Steps[0].String() != "zpool remove -- tank /dev/log1" || mutate.TierOf(p) != mutate.Medium || p.Irreversible != nil {
		t.Errorf("log: blocked %v, steps %v, tier %v", p.Blocked, p.Steps, mutate.TierOf(p))
	}
}

func TestVdevRemoveRefusals(t *testing.T) {
	full := twoMirrors()
	full.topo["tank"].Data.Children[0].Alloc = 4 * tib
	full.topo["tank"].Data.Children[1].Alloc = 4 * tib
	only := mirrorPool("mirror")
	removing := twoMirrors()
	st := removing.topo["tank"]
	st.Removal = &zfs.Removal{State: "SCANNING", Vdev: 1}
	removing.topo["tank"] = st
	cases := []struct {
		name string
		f    *fakeReader
		dev  string
		want string
	}{
		{"raidz", mirrorPool("raidz2"), "/dev/x1", "out of tank, which has raidz"},
		{"only vdev", only, "/dev/x1", "mirror-0 is the only data vdev of tank"},
		{"no room", full, "/dev/x1", "4 TiB and the other data vdevs have 0 B free"},
		{"one at a time", removing, "/dev/x1", "mirror-1 is being removed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Builder{R: c.f}.VdevRemove(t.Context(), "tank", c.dev)
			if err != nil || !strings.Contains(blocked(p), c.want) {
				t.Errorf("blocked %q, %v; want %q", blocked(p), err, c.want)
			}
		})
	}
	// The only vdev is refused for that alone, not also for lack of room.
	only.topo["tank"].Data.Children[0].Alloc = tib
	if p, _ := (Builder{R: only}).VdevRemove(t.Context(), "tank", "/dev/x1"); len(p.Blocked) != 1 {
		t.Errorf("only vdev: %q", p.Blocked)
	}
}

func TestVdevRemoveCancel(t *testing.T) {
	p, _ := Builder{R: twoMirrors()}.VdevRemoveCancel(t.Context(), "tank")
	if !strings.Contains(blocked(p), "tank has no vdev being removed") {
		t.Errorf("nothing running: %q", blocked(p))
	}
	f := twoMirrors()
	st := f.topo["tank"]
	st.Removal = &zfs.Removal{State: "SCANNING", Vdev: 1}
	f.topo["tank"] = st
	p, err := Builder{R: f}.VdevRemoveCancel(t.Context(), "tank")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool remove -s -- tank" || !strings.Contains(p.Preview.Affects[0].Raw(), "mirror-1 stays") {
		t.Errorf("plan %+v, %v", p, err)
	}
}

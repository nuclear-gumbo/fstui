// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// withMembers is the inventory plus tank's two 4 TiB members.
func withMembers() []disks.Device {
	inv := inventory()
	for _, n := range []string{"x1", "x2"} {
		inv = append(inv, disks.Device{Name: n, Path: "/dev/" + n, Size: 4 * tib, Type: "disk", InPool: "tank"})
	}
	return inv
}

func blocked(p *mutate.Plan) string { return strings.Join(p.Blocked, "; ") }

func TestAttach(t *testing.T) {
	b := creator(mirrorPool("mirror"), withMembers())
	p, err := b.DeviceNew(t.Context(), mutate.DeviceAttach, NewDeviceSpec{Pool: "tank", Device: "/dev/x1", New: id("sda")})
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if p.Steps[0].String() != "zpool attach -- tank /dev/x1 /dev/disk/by-id/ata-sda" || !strings.Contains(p.Preview.Affects[0].Raw(), "3-way mirror") {
		t.Errorf("steps %v, affects %v", p.Steps, p.Preview.Affects)
	}
	if mutate.TierOf(p) != mutate.Medium {
		t.Errorf("tier %v", mutate.TierOf(p))
	}
	// Overwriting a signature forces it: the name is typed.
	p, _ = b.DeviceNew(t.Context(), mutate.DeviceAttach, NewDeviceSpec{Pool: "tank", Device: "/dev/x1", New: id("sde"), Overwrite: true})
	if len(p.Blocked) != 0 || !strings.HasPrefix(p.Steps[0].String(), "zpool attach -f --") || mutate.TierOf(p) != mutate.High {
		t.Errorf("overwrite: blocked %v, steps %v, tier %v", p.Blocked, p.Steps, mutate.TierOf(p))
	}
}

func TestAttachRefusals(t *testing.T) {
	cases := []struct {
		name, layout, dev, new string
		want                   string
	}{
		{"too small", "mirror", "/dev/x1", id("sdd"), "smaller than"},
		{"mounted", "mirror", "/dev/x1", id("sdf"), "mounted"},
		{"no device", "mirror", "/dev/x1", "", "choose the new device"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := creator(mirrorPool(c.layout), withMembers()).DeviceNew(t.Context(), mutate.DeviceAttach,
				NewDeviceSpec{Pool: "tank", Device: c.dev, New: c.new})
			if err != nil || !strings.Contains(blocked(p), c.want) {
				t.Errorf("blocked %q, %v; want %q", blocked(p), err, c.want)
			}
		})
	}
}

func TestReplace(t *testing.T) {
	f := mirrorPool("mirror")
	st := f.topo["tank"]
	st.Data.Children[0].Children[1].State = "FAULTED"
	f.topo["tank"] = st
	p, err := creator(f, withMembers()).DeviceNew(t.Context(), mutate.DeviceReplace, NewDeviceSpec{Pool: "tank", Device: "/dev/x2", New: id("sdb")})
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool replace -- tank /dev/x2 /dev/disk/by-id/ata-sdb" {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if len(p.Preview.Affects) != 2 || !strings.Contains(p.Preview.Affects[1].Raw(), "FAULTED") {
		t.Errorf("affects %v", p.Preview.Affects)
	}
	st.Checkpoint = &zfs.Checkpoint{}
	f.topo["tank"] = st
	p, _ = creator(f, withMembers()).DeviceNew(t.Context(), mutate.DeviceReplace, NewDeviceSpec{Pool: "tank", Device: "/dev/x2", New: id("sdb")})
	if !strings.Contains(blocked(p), "checkpoint") {
		t.Errorf("with a checkpoint: %q", blocked(p))
	}
}

func TestDetach(t *testing.T) {
	b := Builder{R: mirrorPool("mirror")}
	p, err := b.DeviceChange(t.Context(), mutate.DeviceDetach, "tank", "/dev/x1")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool detach -- tank /dev/x1" {
		t.Fatalf("plan %+v, %v", p, err)
	}
	// Two sides: one is left, with no redundancy.
	if !p.Flags.RedundancyLoss || mutate.TierOf(p) != mutate.High {
		t.Errorf("redundancy loss %v, tier %v", p.Flags.RedundancyLoss, mutate.TierOf(p))
	}
	f := mirrorPool("mirror")
	f.topo["tank"].Data.Children[0].Children[1].State = "UNAVAIL"
	p, _ = Builder{R: f}.DeviceChange(t.Context(), mutate.DeviceDetach, "tank", "/dev/x1")
	if !strings.Contains(blocked(p), "no other side") {
		t.Errorf("last healthy side: %q", blocked(p))
	}
	p, _ = Builder{R: mirrorPool("raidz2")}.DeviceChange(t.Context(), mutate.DeviceDetach, "tank", "/dev/x1")
	if !strings.Contains(blocked(p), "/dev/x1 is not a side of a mirror") {
		t.Errorf("raidz member: %q", blocked(p))
	}
}

// The old device of a replacement in progress is not detached; the new
// one may be, which cancels the replacement.
func TestDetachReplacing(t *testing.T) {
	f := mirrorPool("mirror")
	leaf := func(n string) zfs.Vdev {
		return zfs.Vdev{Name: termtext.New(n), Type: "disk", Path: n, State: "ONLINE"}
	}
	f.topo["tank"].Data.Children[0].Children[1] = zfs.Vdev{
		Name: termtext.New("replacing-1"), Type: "replacing", State: "ONLINE", Children: []zfs.Vdev{leaf("/dev/x2"), leaf("/dev/x3")},
	}
	p, _ := Builder{R: f}.DeviceChange(t.Context(), mutate.DeviceDetach, "tank", "/dev/x2")
	if !strings.Contains(blocked(p), "being replaced") {
		t.Errorf("old side: %q", blocked(p))
	}
	p, _ = Builder{R: f}.DeviceChange(t.Context(), mutate.DeviceDetach, "tank", "/dev/x3")
	if len(p.Blocked) != 0 || !strings.Contains(p.Preview.Affects[0].Raw(), "cancelled") {
		t.Errorf("new side: blocked %q, affects %v", blocked(p), p.Preview.Affects)
	}
}

// Attaching to a member of a raidz vdev widens the vdev: zpool is given
// the vdev's name, and it can never be undone.
func TestExpand(t *testing.T) {
	b := creator(mirrorPool("raidz2"), withMembers())
	spec := NewDeviceSpec{Pool: "tank", Device: "/dev/x1", New: id("sda")}
	p, err := b.DeviceNew(t.Context(), mutate.DeviceAttach, spec)
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if p.Steps[0].String() != "zpool attach -- tank raidz2-0 /dev/disk/by-id/ata-sda" || mutate.TierOf(p) != mutate.High || len(p.Irreversible) != 1 {
		t.Errorf("steps %v, tier %v, irreversible %v", p.Steps, mutate.TierOf(p), p.Irreversible)
	}
	if a := p.Preview.Affects[0].Raw(); !strings.Contains(a, "from 2 to 3 devices") || !strings.Contains(a, "about 4 TiB") {
		t.Errorf("affects %q", a)
	}

	f := mirrorPool("raidz2")
	st := f.topo["tank"]
	st.Expansion = &zfs.Expansion{State: "SCANNING"}
	f.topo["tank"] = st
	p, _ = creator(f, withMembers()).DeviceNew(t.Context(), mutate.DeviceAttach, spec)
	if !strings.Contains(blocked(p), "one expansion at a time") {
		t.Errorf("while expanding: %q", blocked(p))
	}
	f = mirrorPool("raidz2")
	f.feature = "disabled"
	p, _ = creator(f, withMembers()).DeviceNew(t.Context(), mutate.DeviceAttach, spec)
	if !strings.Contains(blocked(p), "raidz_expansion feature of tank is disabled") {
		t.Errorf("feature disabled: %q", blocked(p))
	}
}

// Vdev names come from zpool; one without a parity digit counts as no
// redundancy instead of crashing.
func TestSpareCopiesOddNames(t *testing.T) {
	for _, n := range []string{"raidz", "draid", "raidz-0", ""} {
		if got := spareCopies(&zfs.Vdev{Name: termtext.New(n), Type: "raidz"}); got != 0 {
			t.Errorf("%q: %d", n, got)
		}
	}
}

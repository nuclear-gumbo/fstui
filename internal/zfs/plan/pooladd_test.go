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

// mirrorPool is tank, one mirror of two devices.
func mirrorPool(layout string) *fakeReader {
	leaf := func(n string) zfs.Vdev {
		return zfs.Vdev{Name: termtext.New(n), Type: "disk", Path: n, State: "ONLINE"}
	}
	top := zfs.Vdev{Name: termtext.New("mirror-0"), Type: "mirror", Children: []zfs.Vdev{leaf("/dev/x1"), leaf("/dev/x2")}}
	if layout == "raidz2" {
		top = zfs.Vdev{Name: termtext.New("raidz2-0"), Type: "raidz", Children: top.Children}
	}
	st := zfs.PoolStatus{GUID: 7, Data: zfs.Vdev{Name: termtext.New("tank"), Type: "root", Children: []zfs.Vdev{top}}}
	return &fakeReader{topo: map[string]zfs.PoolStatus{"tank": st}}
}

func TestPoolAddMirror(t *testing.T) {
	p, err := creator(mirrorPool("mirror"), inventory()).PoolAdd(t.Context(), AddSpec{
		Pool: "tank", Layout: "mirror", Data: []string{id("sda"), id("sdb")}, Checkpoint: true,
	})
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if len(p.Steps) != 2 || p.Steps[0].String() != "zpool checkpoint -- tank" ||
		p.Steps[1].String() != "zpool add -- tank mirror /dev/disk/by-id/ata-sda /dev/disk/by-id/ata-sdb" {
		t.Errorf("steps %v", p.Steps)
	}
	if mutate.TierOf(p) != mutate.High || len(p.Irreversible) != 1 || !strings.Contains(p.Irreversible[0], "zpool remove") {
		t.Errorf("tier %v, irreversible %v", mutate.TierOf(p), p.Irreversible)
	}
	if p.Baseline["pool tank (data vdevs)"] != "mirror x1" {
		t.Errorf("baseline %v", p.Baseline)
	}
}

// A log device can be removed again: Medium, nothing irreversible.
func TestPoolAddLog(t *testing.T) {
	p, err := creator(mirrorPool("mirror"), inventory()).PoolAdd(t.Context(), AddSpec{Pool: "tank", Log: []string{id("sdc")}})
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if mutate.TierOf(p) != mutate.Medium || p.Irreversible != nil {
		t.Errorf("tier %v, irreversible %v", mutate.TierOf(p), p.Irreversible)
	}
}

func TestPoolAddRefusals(t *testing.T) {
	cases := []struct {
		name string
		pool string
		s    AddSpec
		want string
	}{
		{"nothing", "mirror", AddSpec{Pool: "tank", Layout: "mirror"}, "at least one device"},
		{"mismatch", "mirror", AddSpec{Pool: "tank", Data: []string{id("sda")}}, "the data vdevs of tank are mirror"},
		{"weaker raidz", "raidz2", AddSpec{Pool: "tank", Layout: "raidz1", Data: []string{id("sda"), id("sdb"), id("sdc")}}, "raidz2"},
		{"single special", "mirror", AddSpec{Pool: "tank", Special: []string{id("sda")}}, "single special"},
		{"mounted", "mirror", AddSpec{Pool: "tank", Log: []string{id("sdf")}}, "mounted"},
		{"signature", "mirror", AddSpec{Pool: "tank", Cache: []string{id("sde")}}, "switch on overwrite"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := creator(mirrorPool(c.pool), inventory()).PoolAdd(t.Context(), c.s)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(p.Blocked, "; "), c.want) {
				t.Errorf("blocked %v, want %q", p.Blocked, c.want)
			}
		})
	}
}

// Overwrite passes -f only because a device holds a signature; adding to
// a raidz pool is irreversible.
func TestPoolAddOverwriteRaidz(t *testing.T) {
	p, err := creator(mirrorPool("raidz2"), inventory()).PoolAdd(t.Context(), AddSpec{
		Pool: "tank", Layout: "raidz2", Data: []string{id("sda"), id("sdb"), id("sde")}, Overwrite: true,
	})
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if got := p.Steps[0].String(); !strings.HasPrefix(got, "zpool add -f -- tank raidz2 ") {
		t.Errorf("command %s", got)
	}
	if len(p.Irreversible) != 1 || !strings.Contains(p.Irreversible[0], "never be removed") {
		t.Errorf("irreversible %v", p.Irreversible)
	}
}

func TestPoolAddUnprivileged(t *testing.T) {
	b := creator(mirrorPool("mirror"), inventory())
	b.Unprivileged = true
	p, err := b.PoolAdd(t.Context(), AddSpec{Pool: "tank", Log: []string{id("sdc")}})
	if err != nil || !strings.Contains(strings.Join(p.Blocked, ";"), "needs root") {
		t.Errorf("blocked %v, %v", p.Blocked, err)
	}
}

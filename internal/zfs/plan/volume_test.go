// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func volWorld() Builder {
	r := dsWorld()
	r.ids["tank/vm"] = zfs.Identity{GUID: "20", CreateTXG: "20"}
	r.props["tank/vm"] = map[string]string{"type": "volume", "volsize": "10737418240", "volblocksize": "16384"}
	return Builder{R: r}
}

func TestVolumeCreate(t *testing.T) {
	b := volWorld()
	p, err := b.VolumeCreate(t.Context(), "tank/new", "20G", true, "16K")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zfs create -s -V 21474836480 -b 16384 -- tank/new" {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if !strings.Contains(p.Summary, "20 GiB") || mutate.TierOf(p) != mutate.Low {
		t.Errorf("summary %q, tier %s", p.Summary, mutate.TierOf(p))
	}
	for _, bad := range [][2]string{{"0", ""}, {"lots", ""}, {"20G", "3K"}, {"10000", "16K"}} {
		if _, err := b.VolumeCreate(t.Context(), "tank/new", bad[0], false, bad[1]); err == nil {
			t.Errorf("size %q block %q accepted", bad[0], bad[1])
		}
	}
}

func TestVolumeResize(t *testing.T) {
	b := volWorld()
	grow, err := b.VolumeResize(t.Context(), "tank/vm", "20G")
	if err != nil || mutate.TierOf(grow) != mutate.Medium || grow.Steps[0].String() != "zfs set -- volsize=21474836480 tank/vm" {
		t.Fatalf("grow %+v, %v", grow, err)
	}
	shrink, err := b.VolumeResize(t.Context(), "tank/vm", "5G")
	if err != nil || mutate.TierOf(shrink) != mutate.High || len(shrink.Irreversible) == 0 {
		t.Fatalf("shrink %+v, %v", shrink, err)
	}
	same, _ := b.VolumeResize(t.Context(), "tank/vm", "10G")
	odd, _ := b.VolumeResize(t.Context(), "tank/vm", "10241")
	if len(same.Blocked) == 0 || len(odd.Blocked) == 0 {
		t.Errorf("same %q, odd %q", same.Blocked, odd.Blocked)
	}
	if _, err := b.VolumeResize(t.Context(), "tank/a", "1G"); err == nil {
		t.Error("resized a filesystem")
	}
	q, err := b.Build(t.Context(), mutate.VolumeResize, shrink.Params)
	if err != nil || q.Summary != shrink.Summary {
		t.Errorf("rebuilt %+v, %v", q, err)
	}
}

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

func vdev(name, typ, state string, children ...zfs.Vdev) zfs.Vdev {
	return zfs.Vdev{Name: termtext.New(name), Type: typ, State: state, Children: children}
}

func devWorld(group zfs.Vdev) Builder {
	r := world()
	r.topo = map[string]zfs.PoolStatus{"tank": {GUID: 9, Data: vdev("tank", "root", "ONLINE", group)}}
	return Builder{R: r}
}

func TestOfflineRedundancy(t *testing.T) {
	mirror := vdev("mirror-0", "mirror", "ONLINE", vdev("sda", "disk", "ONLINE"), vdev("sdb", "disk", "ONLINE"))
	degraded := vdev("mirror-0", "mirror", "DEGRADED", vdev("sda", "disk", "OFFLINE"), vdev("sdb", "disk", "ONLINE"))
	raidz2 := vdev("raidz2-0", "raidz", "ONLINE", vdev("a", "disk", "ONLINE"), vdev("b", "disk", "ONLINE"), vdev("c", "disk", "ONLINE"), vdev("d", "disk", "ONLINE"))
	single := vdev("sdz", "disk", "ONLINE")
	tests := []struct {
		name    string
		group   zfs.Vdev
		dev     string
		tier    mutate.Tier
		blocked string
	}{
		{"two-way mirror", mirror, "sda", mutate.High, ""},
		{"mirror already degraded", degraded, "sdb", mutate.High, "no valid replicas"},
		{"raidz2", raidz2, "a", mutate.Medium, ""},
		{"single disk", single, "sdz", mutate.High, "no valid replicas"},
		{"already offline", degraded, "sda", mutate.High, "already offline"},
	}
	for _, tt := range tests {
		p, err := devWorld(tt.group).DeviceChange(t.Context(), mutate.DeviceOffline, "tank", tt.dev)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		got := strings.Join(p.Blocked, "; ")
		if tt.blocked == "" && got != "" || tt.blocked != "" && !strings.Contains(got, tt.blocked) {
			t.Errorf("%s: blocked %q, want %q", tt.name, got, tt.blocked)
		}
		if tt.blocked == "" && mutate.TierOf(p) != tt.tier {
			t.Errorf("%s: tier %s, want %s", tt.name, mutate.TierOf(p), tt.tier)
		}
	}
}

func TestOnlineAndClear(t *testing.T) {
	b := devWorld(vdev("mirror-0", "mirror", "DEGRADED", vdev("sda", "disk", "OFFLINE"), vdev("sdb", "disk", "ONLINE")))
	p, err := b.DeviceChange(t.Context(), mutate.DeviceOnline, "tank", "sda")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool online -- tank sda" {
		t.Fatalf("online: %+v, %v", p, err)
	}
	if p, _ := b.DeviceChange(t.Context(), mutate.DeviceOnline, "tank", "sdb"); len(p.Blocked) != 1 {
		t.Errorf("online an online device: %q", p.Blocked)
	}
	c, err := b.DeviceChange(t.Context(), mutate.DeviceClear, "tank", "sdb")
	if err != nil || c.Steps[0].String() != "zpool clear -- tank sdb" || mutate.TierOf(c) != mutate.Low {
		t.Fatalf("clear: %+v, %v", c, err)
	}
	if _, err := b.DeviceChange(t.Context(), mutate.DeviceClear, "tank", "nope"); err == nil {
		t.Error("a device that is not in the pool was accepted")
	}
	q, err := b.Build(t.Context(), mutate.DeviceOnline, p.Params)
	if err != nil || q.Summary != p.Summary {
		t.Errorf("rebuilt %+v, %v", q, err)
	}
}

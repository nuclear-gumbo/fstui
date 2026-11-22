// SPDX-License-Identifier: GPL-3.0-or-later

package pve

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func recorded(t *testing.T, file string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "pve", "9.2", file))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMembersRecorded(t *testing.T) {
	m, err := ParseMembers(recorded(t, "members.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Self != "pve1" || m.Cluster != "lab" || !m.Quorate || len(m.Nodes) != 4 {
		t.Fatalf("%+v", m)
	}
	var names []string
	for _, n := range m.Nodes {
		names = append(names, n.Name)
		if !n.Online || !n.IP.IsValid() {
			t.Errorf("%+v", n)
		}
	}
	if !slices.Equal(names, []string{"pve1", "pve2", "pve4", "store1"}) {
		t.Errorf("nodes %v", names)
	}
	if m.Nodes[2].IP.String() != "192.0.2.14" || m.Nodes[2].ID != 3 {
		t.Errorf("pve4 = %+v", m.Nodes[2])
	}
}

// A node outside a cluster writes only its name and a version
// (cfs_create_memberlist_msg, src/pmxcfs/status.c).
func TestMembersStandalone(t *testing.T) {
	m, err := ParseMembers([]byte("{\n\"nodename\": \"pve9\",\n\"version\": 3\n}\n"))
	if err != nil || m.Cluster != "" || len(m.Nodes) != 1 || m.Nodes[0].Name != "pve9" {
		t.Errorf("%+v %v", m, err)
	}
}

func TestMembersRefused(t *testing.T) {
	for _, b := range []string{
		``, `[]`, `{"nodename": ""}`, `{"nodename": "-oProxyCommand=x"}`,
		`{"nodename": "a", "cluster": {"name": "c"}, "nodelist": {"b;id": {"id": 1}}}`,
		`{"nodename": "a", "cluster": {"name": "c"}, "nodelist": {"b": {"id": 1, "ip": "10.0.0.300"}}}`,
	} {
		if _, err := ParseMembers([]byte(b)); !errors.Is(err, ErrMembers) {
			t.Errorf("%q accepted: %v", b, err)
		}
	}
}

func TestStorageRecorded(t *testing.T) {
	ss, err := ParseStorage(recorded(t, "storage.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	if want := []string{"local-zfs", "pbs", "mediapool", "nvmepool", "vmpool", "backup8", "tank-pbs"}; !slices.Equal(ids, want) {
		t.Fatalf("ids %v, want %v (pbs, dir and nfs sections are not ZFS)", ids, want)
	}
	local := ss[0]
	if local.Pool.String() != "rpool" || local.Dataset.String() != "rpool/data" || !slices.Equal(local.Nodes, []string{"pve1", "pve2", "store1"}) {
		t.Errorf("local-zfs = %+v", local)
	}
	if !local.On("pve2") || local.On("pve4") {
		t.Error("local-zfs is on pve1, pve2 and store1 only")
	}
	if tank := ss[6]; tank.Pool.String() != "tank" || tank.Dataset.String() != "tank/pbs" {
		t.Errorf("tank-pbs = %+v", tank)
	}
}

// Sections as SectionConfig reads them: comments skipped, a blank line
// ending a section, a storage without nodes on every node, ZFS over
// iSCSI on its portal and not on a node. The zfs section's keys are
// ZFSPlugin's (pve-storage src/PVE/Storage/ZFSPlugin.pm).
func TestStorageSections(t *testing.T) {
	cfg := "# a comment\nzfspool: all\n\tpool tank\n  # indented comment\n\tdisable 1\n\nzfs: box\n\tportal 192.0.2.50\n\ttarget iqn.2026-11.org.example:box\n\tpool data/vm\n\tiscsiprovider LIO\n\tlio_tpg tpg1\n"
	ss, err := ParseStorage([]byte(cfg))
	if err != nil || len(ss) != 2 {
		t.Fatalf("%+v %v", ss, err)
	}
	if !ss[0].On("any") || !ss[0].Disable {
		t.Errorf("all = %+v", ss[0])
	}
	if ss[1].Type != "zfs" || ss[1].Portal != "192.0.2.50" || ss[1].On("pve1") {
		t.Errorf("box = %+v", ss[1])
	}
}

func TestStorageRefused(t *testing.T) {
	for _, cfg := range []string{
		"zfspool x\n\tpool tank\n",
		"zfspool: x\n\tpool -tank\n",
		"zfspool: x\n\tpool tank\n\tnodes pve1,$(id)\n",
		"zfspool: x\n\tcontent images\n",
	} {
		if _, err := ParseStorage([]byte(cfg)); err == nil {
			t.Errorf("accepted %q", strings.TrimSpace(cfg))
		}
	}
}

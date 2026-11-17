// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/storage"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

func neutralGolden(t *testing.T, scenario string) storage.PoolStatus {
	t.Helper()
	st, err := neutralStatus(onePool(t, scenario))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestNeutralPools(t *testing.T) {
	pools, err := ParsePoolList(golden(t, "degraded", "zpool-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	np, err := neutralPools(pools)
	if err != nil {
		t.Fatal(err)
	}
	if len(np) != 1 || np[0].Health != (storage.Health{Condition: storage.Degraded, Word: "DEGRADED"}) {
		t.Fatalf("pools = %+v, want one DEGRADED pool", np)
	}
	// zpoolprops(7): free is the unallocated part of size.
	if p := np[0]; p.Size == 0 || p.Allocated+p.Free != p.Size {
		t.Fatalf("size %d, allocated %d, free %d: allocated + free != size", p.Size, p.Allocated, p.Free)
	}
}

func TestNeutralStatusRoles(t *testing.T) {
	st := neutralGolden(t, "roles")
	type top struct {
		role storage.Role
		kind string
		word string
	}
	want := []top{
		{storage.Data, "raidz", "ONLINE"},
		{storage.Metadata, "mirror", "ONLINE"},
		{storage.Log, "file", "ONLINE"},
		{storage.Cache, "file", "ONLINE"},
		{storage.Spare, "file", "AVAIL"},
	}
	if len(st.Devices) != len(want) {
		t.Fatalf("%d top-level devices, want %d", len(st.Devices), len(want))
	}
	for i, w := range want {
		d := st.Devices[i]
		if d.Role != w.role || d.Kind != w.kind || d.Health.Word != w.word {
			t.Errorf("device %d = %v %s %s, want %v %s %s", i, d.Role, d.Kind, d.Health.Word, w.role, w.kind, w.word)
		}
	}
	if !st.Devices[4].Health.OK() {
		t.Error("an available spare is reported as a problem")
	}
	for _, c := range st.Devices[1].Children {
		if c.Role != storage.Metadata {
			t.Errorf("special mirror member %s has role %v, want Metadata", c.Name.Raw(), c.Role)
		}
	}
}

func TestNeutralStatusDegraded(t *testing.T) {
	st := neutralGolden(t, "degraded")
	if st.Health.Condition != storage.Degraded || st.Health.OK() {
		t.Fatalf("health = %+v, want Degraded", st.Health)
	}
	for _, m := range st.Devices[0].Children {
		want := storage.Online
		if filepath.Base(m.Name.Raw()) == "fstuitest_g_deg-b.img" {
			want = storage.Offline
		}
		if m.Health.Condition != want {
			t.Errorf("%s condition = %v, want %v", m.Name.Raw(), m.Health.Condition, want)
		}
	}
}

func TestNeutralScan(t *testing.T) {
	for _, tc := range []struct {
		scenario string
		word     string
		state    storage.ScanState
	}{
		{"scrub-in-progress", "SCRUB", storage.Running},
		{"scrub-paused", "SCRUB", storage.Paused},
		{"resilver-in-progress", "RESILVER", storage.Running},
		{"checksum-repaired", "SCRUB", storage.Finished},
	} {
		sc := neutralGolden(t, tc.scenario).Scan
		if sc == nil || sc.Word != tc.word || sc.State != tc.state {
			t.Errorf("%s: scan = %+v, want %s in state %v", tc.scenario, sc, tc.word, tc.state)
			continue
		}
		if tc.state == storage.Finished && sc.End.IsZero() || tc.state != storage.Finished && !sc.End.IsZero() {
			t.Errorf("%s: end time %v does not match state %v", tc.scenario, sc.End, sc.State)
		}
		if sc.Done > sc.Total {
			t.Errorf("%s: done %d exceeds total %d", tc.scenario, sc.Done, sc.Total)
		}
	}
	if sc := neutralGolden(t, "checksum-repaired").Scan; sc.Errors != 0 {
		t.Errorf("checksum-repaired: %d unrepaired errors", sc.Errors)
	}
	if sc := neutralGolden(t, "healthy").Scan; sc != nil {
		t.Errorf("healthy pool was never scrubbed, got scan %+v", sc)
	}
}

func TestNeutralScanUnknownState(t *testing.T) {
	if _, err := neutralScan(Scan{Function: "SCRUB", State: "REWINDING"}); err == nil || !strings.Contains(err.Error(), "REWINDING") {
		t.Fatalf("err = %v, want one naming the unknown state", err)
	}
}

func TestHealthUnknownWord(t *testing.T) {
	h := health("SUSPENDED")
	if h.Condition != storage.Unknown || h.Word != "SUSPENDED" || h.OK() {
		t.Fatalf("health = %+v, want Unknown keeping the word, not OK", h)
	}
}

func TestNeutralSnapshotsOldestFirst(t *testing.T) {
	all, err := ParseDatasetList(golden(t, "healthy", "zfs-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The golden list was captured with default columns; add creation from
	// zfs get, as the reader's -o list would return it.
	props, err := ParseDatasetGet(golden(t, "healthy", "zfs-get.json"))
	if err != nil {
		t.Fatal(err)
	}
	creation := map[string]Property{}
	for _, d := range props {
		creation[d.Name] = d.Properties["creation"]
	}
	var home []Dataset
	for _, d := range all {
		if d.Type == Snapshot && strings.HasPrefix(d.Name, "fstuitest_g_healthy/home@") {
			d.Properties["creation"] = creation[d.Name]
			home = append(home, d)
		}
	}
	// capture-golden.sh takes daily-1 before daily-2; sort them the other
	// way round so only createtxg ordering can restore it.
	if len(home) < 2 {
		t.Fatalf("%d snapshots of home, want at least 2", len(home))
	}
	home[0], home[1] = home[1], home[0]
	snaps, err := neutralSnapshots(home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(snaps[0].Name.Raw(), "@daily-1") || !strings.HasSuffix(snaps[1].Name.Raw(), "@daily-2") {
		t.Fatalf("order = %s, %s; want daily-1 then daily-2", snaps[0].Name.Raw(), snaps[1].Name.Raw())
	}
	if want := time.Unix(int64(creation[snaps[0].Name.Raw()].Number), 0); !snaps[0].Created.Equal(want) { //nolint:gosec // test data
		t.Fatalf("created = %v, want %v", snaps[0].Created, want)
	}
}

func TestNumbersRejectsText(t *testing.T) {
	var n uint64
	p := Properties{"used": {Text: termtext.New("-")}}
	if err := p.numbers(map[string]*uint64{"used": &n}); err == nil {
		t.Fatal(`"-" accepted as a byte count`)
	}
	if err := p.numbers(map[string]*uint64{"referenced": &n}); err == nil {
		t.Fatal("missing property accepted")
	}
}

// Names come back from the UI as storage values; a name that is not valid
// ZFS must be refused before a command is built. The Reader has no runner,
// so reaching the command would panic.
func TestReaderRejectsInvalidNames(t *testing.T) {
	r := &Reader{}
	if _, err := r.PoolStatus(t.Context(), storage.Pool{Name: termtext.New("-o")}); err == nil {
		t.Error("pool name -o accepted")
	}
	if _, err := r.Filesystems(t.Context(), storage.Pool{Name: termtext.New("tank/child")}); err == nil {
		t.Error("dataset name accepted as a pool")
	}
	if _, err := r.Snapshots(t.Context(), storage.Filesystem{Name: termtext.New("tank/a@b")}); err == nil {
		t.Error("snapshot name accepted as a filesystem")
	}
}

func TestJoinOverviews(t *testing.T) {
	pools, err := ParsePoolList(golden(t, "healthy", "zpool-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := []PoolStatus{onePool(t, "healthy")}
	ds, err := ParseDatasetList(golden(t, "healthy", "zfs-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := joinOverviews(pools, st, ds)
	if err != nil {
		t.Fatal(err)
	}
	root := ds[0].Properties
	if len(got) != 1 || got[0].Used != root["used"].Number || got[0].Available != root["available"].Number || got[0].Status.Name != pools[0].Name {
		t.Fatalf("overview = %+v", got)
	}
	// A pool exported between zpool list and zpool status is left out,
	// not shown half-read.
	if got, err := joinOverviews(pools, nil, ds); err != nil || len(got) != 0 {
		t.Fatalf("pool without status: %+v, %v", got, err)
	}
}

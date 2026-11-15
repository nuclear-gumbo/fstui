// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golden reads captured OpenZFS 2.3.9 output (testdata/zfs/2.3.9/README.md
// describes how each scenario was built). Expected values below come from
// those scenario definitions, not from running the parser.
func golden(t *testing.T, parts ...string) []byte {
	t.Helper()
	p := filepath.Join(append([]string{"..", "..", "testdata", "zfs", "2.3.9"}, parts...)...)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func onePool(t *testing.T, scenario string) PoolStatus {
	t.Helper()
	pools, err := ParsePoolStatus(golden(t, scenario, "zpool-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pools) != 1 {
		t.Fatalf("%s: %d pools, want 1", scenario, len(pools))
	}
	return pools[0]
}

func leafNames(v Vdev) []string {
	if len(v.Children) == 0 {
		return []string{filepath.Base(v.Name.Raw())}
	}
	var out []string
	for _, c := range v.Children {
		out = append(out, leafNames(c)...)
	}
	return out
}

func TestParseVersion(t *testing.T) {
	v, err := ParseVersion(golden(t, "zfs-version.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := Release{2, 3, 9}
	if v.UserlandRelease != want || v.KernelRelease != want {
		t.Fatalf("releases = %v / %v, want %v", v.UserlandRelease, v.KernelRelease, want)
	}
	if v.Mismatched() {
		t.Fatal("matching userland and kernel reported as mismatched")
	}
	if !v.UserlandRelease.AtLeast(Release{2, 3, 0}) || v.UserlandRelease.AtLeast(Release{2, 4, 0}) {
		t.Fatal("AtLeast ordering is wrong")
	}
}

func TestVersionMismatchAndSuffixes(t *testing.T) {
	doc := `{"output_version":{"command":"zfs version","vers_major":0,"vers_minor":1},` +
		`"zfs_version":{"userland":"zfs-2.3.9-r1-gentoo","kernel":"zfs-kmod-2.3.8-1"}}`
	v, err := ParseVersion([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Mismatched() || v.KernelRelease != (Release{2, 3, 8}) {
		t.Fatalf("got %+v, want a 2.3.9 / 2.3.8 mismatch", v)
	}
}

func TestDecodeRejectsWrongDocumentAndVersion(t *testing.T) {
	_, err := ParsePoolStatus(golden(t, "healthy", "zpool-list.json"))
	if err == nil || !strings.Contains(err.Error(), `"zpool list"`) {
		t.Fatalf("parsing zpool list output as status: err = %v", err)
	}
	doc := `{"output_version":{"command":"zfs version","vers_major":1,"vers_minor":0},"zfs_version":{}}`
	if _, err := ParseVersion([]byte(doc)); !errors.Is(err, ErrOutputVersion) {
		t.Fatalf("major version 1: err = %v, want ErrOutputVersion", err)
	}
}

func TestStatusDegraded(t *testing.T) {
	p := onePool(t, "degraded")
	if p.State != "DEGRADED" {
		t.Fatalf("pool state = %s", p.State)
	}
	raidz := p.Data.Children[0]
	if raidz.Type != "raidz" || raidz.State != "DEGRADED" {
		t.Fatalf("top vdev = %s %s, want raidz DEGRADED", raidz.Type, raidz.State)
	}
	states := map[string]string{}
	for _, c := range raidz.Children {
		states[filepath.Base(c.Name.Raw())] = c.State
	}
	if states["fstuitest_g_deg-b.img"] != "OFFLINE" || states["fstuitest_g_deg-a.img"] != "ONLINE" {
		t.Fatalf("member states = %v, want deg-b OFFLINE", states)
	}
	if len(p.Status) == 0 || !strings.Contains(p.Status[0].Display(), "taken offline") {
		t.Fatalf("Status = %v, want the offline explanation", p.Status)
	}
	for _, l := range append(p.Status, p.Action...) {
		if strings.ContainsAny(l.Raw(), "\n\t") {
			t.Fatalf("explanation line still contains layout characters: %q", l.Raw())
		}
	}
}

func TestStatusChecksumRepaired(t *testing.T) {
	p := onePool(t, "checksum-repaired")
	b := p.Data.Children[0].Children[1]
	if filepath.Base(b.Name.Raw()) != "fstuitest_g_ck-b.img" {
		t.Fatalf("second mirror member = %s, want ck-b (order lost?)", b.Name.Raw())
	}
	if b.ChecksumErrors != 771 || b.SelfHealed == 0 {
		t.Fatalf("ck-b checksum errors %d, self-healed %d; want 771 and >0", b.ChecksumErrors, b.SelfHealed)
	}
	if p.Scan == nil || p.Scan.Function != "SCRUB" || p.Scan.State != "FINISHED" || p.Scan.Errors != 0 || p.Scan.End.IsZero() {
		t.Fatalf("scan = %+v, want a finished scrub with no unrepaired errors", p.Scan)
	}
}

func TestStatusScrubPausedVersusRunning(t *testing.T) {
	running := onePool(t, "scrub-in-progress").Scan
	paused := onePool(t, "scrub-paused").Scan
	if running.State != "SCANNING" || running.Paused {
		t.Fatalf("running scrub = %+v", running)
	}
	// A paused scrub keeps state SCANNING; only scrub_pause tells.
	if paused.State != "SCANNING" || !paused.Paused || paused.PausedAt.Before(paused.Start) {
		t.Fatalf("paused scrub = %+v", paused)
	}
}

func TestStatusResilverReplacing(t *testing.T) {
	p := onePool(t, "resilver-in-progress")
	if p.Scan == nil || p.Scan.Function != "RESILVER" || p.Scan.State != "SCANNING" {
		t.Fatalf("scan = %+v, want a resilver in progress", p.Scan)
	}
	repl := p.Data.Children[0].Children[1]
	if repl.Type != "replacing" || strings.Join(leafNames(repl), ",") != "fstuitest_g_rs-b.img,fstuitest_g_rs-c.img" {
		t.Fatalf("replacing vdev = %s %v", repl.Type, leafNames(repl))
	}
}

func TestStatusVdevRoles(t *testing.T) {
	p := onePool(t, "roles")
	if got := strings.Join(leafNames(p.Data), ","); got != "fstuitest_g_roles-a.img,fstuitest_g_roles-b.img,fstuitest_g_roles-c.img" {
		t.Fatalf("data leaves = %s", got)
	}
	if len(p.Special) != 1 || p.Special[0].Type != "mirror" || len(p.Special[0].Children) != 2 {
		t.Fatalf("special = %+v, want one two-way mirror", p.Special)
	}
	if len(p.Logs) != 1 || len(p.Cache) != 1 || len(p.Spares) != 1 || len(p.Dedup) != 0 {
		t.Fatalf("logs/cache/spares/dedup = %d/%d/%d/%d, want 1/1/1/0", len(p.Logs), len(p.Cache), len(p.Spares), len(p.Dedup))
	}
	if p.Spares[0].State != "AVAIL" {
		t.Fatalf("spare state = %s, want AVAIL", p.Spares[0].State)
	}
}

func TestPoolListKeepsExactGUIDAndNumbers(t *testing.T) {
	pools, err := ParsePoolList(golden(t, "healthy", "zpool-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := pools[0]
	// Above 2^53: a float64 decode would round this.
	if p.GUID != 3514380900511600565 {
		t.Fatalf("GUID = %d", p.GUID)
	}
	size := p.Properties["size"]
	if !size.IsNumber || size.Number != 503316480 || size.Source.Kind != SourceNone {
		t.Fatalf("size = %+v", size)
	}
	if h := p.Properties["health"]; h.IsNumber || h.Text.Raw() != "ONLINE" {
		t.Fatalf("health = %+v", h)
	}
}

func TestDatasetGetHealthy(t *testing.T) {
	ds, err := ParseDatasetGet(golden(t, "healthy", "zfs-get.json"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Dataset{}
	for _, d := range ds {
		byName[d.Name] = d
	}
	user := byName["fstuitest_g_healthy/home/user"].Properties
	if c := user["compression"]; c.Text.Raw() != "zstd" || c.Source.Kind != SourceInherited || c.Source.From.Raw() != "fstuitest_g_healthy/home" {
		t.Fatalf("home/user compression = %+v, want zstd inherited from home", c)
	}
	note := user["com.example:note"]
	if !strings.ContainsRune(note.Text.Raw(), 0x1b) || strings.ContainsRune(note.Text.Display(), 0x1b) {
		t.Fatalf("hostile note must keep raw bytes and display escaped: raw %q display %q", note.Text.Raw(), note.Text.Display())
	}
	if ks := byName["fstuitest_g_healthy/locked"].Properties["keystatus"]; ks.Text.Raw() != "unavailable" {
		t.Fatalf("locked keystatus = %q", ks.Text.Raw())
	}
	if q := user["quota"]; !q.IsNumber || q.Number != 0 || q.Source.Kind != SourceDefault {
		t.Fatalf("quota = %+v, want default 0", q)
	}
}

func TestDatasetListTypesAndOrder(t *testing.T) {
	ds, err := ParseDatasetList(golden(t, "healthy", "zfs-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	types := map[DatasetType]int{}
	for i, d := range ds {
		types[d.Type]++
		if i > 0 && ds[i-1].Name >= d.Name {
			t.Fatalf("not sorted: %s before %s", ds[i-1].Name, d.Name)
		}
	}
	// Scenario: 5 filesystems besides the root (home, home/user, clone,
	// secret, locked) plus the root, 1 volume, 4 snapshots (daily-1 and
	// daily-2 of home and home/user), 1 bookmark.
	if types[Filesystem] != 6 || types[Volume] != 1 || types[Snapshot] != 4 || types[Bookmark] != 1 {
		t.Fatalf("type counts = %v", types)
	}
	snap := ds[indexOf(ds, "fstuitest_g_healthy/home@daily-2")]
	if a := snap.Properties["available"]; a.IsNumber || a.Text.Raw() != "-" {
		t.Fatalf("snapshot available = %+v, want the string \"-\"", a)
	}
}

func indexOf(ds []Dataset, n string) int {
	for i, d := range ds {
		if d.Name == n {
			return i
		}
	}
	return -1
}

func TestParseRejectsInvalidNamesAndValues(t *testing.T) {
	head := `{"output_version":{"command":"zfs list","vers_major":0,"vers_minor":1},"datasets":{`
	tests := map[string]string{
		"control character in name": `"a":{"name":"tank/a\u001bb","type":"FILESYSTEM","pool":"tank","properties":{}}}}`,
		"snapshot name without @":   `"a":{"name":"tank/a","type":"SNAPSHOT","pool":"tank","properties":{}}}}`,
		"unknown type":              `"a":{"name":"tank/a","type":"WIDGET","pool":"tank","properties":{}}}}`,
		"boolean value":             `"a":{"name":"tank/a","type":"FILESYSTEM","pool":"tank","properties":{"x":{"value":true,"source":{"type":"NONE","data":"-"}}}}}}`,
		"negative number":           `"a":{"name":"tank/a","type":"FILESYSTEM","pool":"tank","properties":{"x":{"value":-1,"source":{"type":"NONE","data":"-"}}}}}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDatasetList([]byte(head + body)); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestStatusCheckpoint(t *testing.T) {
	p := onePool(t, "checkpoint")
	// capture-golden.sh deletes a 32 MiB file after the checkpoint, so the
	// checkpoint holds at least that much.
	if c := p.Checkpoint; c == nil || c.State != "EXISTS" || c.Taken.IsZero() || c.Space < 32<<20 {
		t.Fatalf("checkpoint = %+v, want EXISTS holding at least 32 MiB", c)
	}
	if onePool(t, "healthy").Checkpoint != nil {
		t.Fatal("pool without a checkpoint reports one")
	}
}

func TestPoolListWithNoPools(t *testing.T) {
	pools, err := ParsePoolList(golden(t, "zpool-list-none.json"))
	if err != nil || len(pools) != 0 {
		t.Fatalf("no pools: %v, %v", pools, err)
	}
}

// With -p, numbers arrive as exact digit strings; they must count as
// numbers, including GUIDs above 2^63-1, which --json-int clamps.
func TestDigitStringsAreNumbers(t *testing.T) {
	doc := `{"output_version":{"command":"zfs get","vers_major":0,"vers_minor":1},"datasets":{"p/a":{"name":"p/a","type":"FILESYSTEM","pool":"p","createtxg":"8",` +
		`"properties":{"guid":{"value":"16080832919552626521","source":{"type":"NONE","data":"-"}},` +
		`"used":{"value":"125952","source":{"type":"NONE","data":"-"}},` +
		`"compressratio":{"value":"1.00","source":{"type":"NONE","data":"-"}}}}}}`
	ds, err := ParseDatasetGet([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	p := ds[0].Properties
	if g := p["guid"]; !g.IsNumber || g.Number != 16080832919552626521 {
		t.Errorf("guid = %+v", g)
	}
	if u := p["used"]; !u.IsNumber || u.Number != 125952 {
		t.Errorf("used = %+v", u)
	}
	if r := p["compressratio"]; r.IsNumber || r.Text.Raw() != "1.00" {
		t.Errorf("compressratio = %+v", r)
	}
}

// The -p captures carry GUIDs above 2^63-1 exactly; --json-int would have
// printed each of them as 9223372036854775807.
func TestPropertiesWithP(t *testing.T) {
	ds, err := ParseDatasetGet(golden(t, "healthy", "zfs-get-p.json"))
	if err != nil {
		t.Fatal(err)
	}
	above := 0
	for _, d := range ds {
		g := d.Properties["guid"]
		if !g.IsNumber || g.Number == math.MaxInt64 {
			t.Errorf("%s guid = %+v", d.Name, g)
		}
		if g.Number > math.MaxInt64 {
			above++
		}
		if u := d.Properties["used"]; d.Type != Bookmark && !u.IsNumber {
			t.Errorf("%s used is not a number: %+v", d.Name, u)
		}
	}
	if above == 0 {
		t.Error("no GUID above 2^63-1 in the capture; the test proves nothing")
	}
	pools, err := ParsePoolGet(golden(t, "healthy", "zpool-get-p.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s := pools[0].Properties["size"]; !s.IsNumber || s.Number == 0 {
		t.Errorf("pool size = %+v", s)
	}
}

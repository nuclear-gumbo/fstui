// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func TestCheckProperty(t *testing.T) {
	good := map[[2]string]string{
		{"compression", "zstd-3"}:      "zstd-3",
		{"compression", "lz4"}:         "lz4",
		{"quota", "10G"}:               "10737418240",
		{"quota", "none"}:              "none",
		{"reservation", "4KiB"}:        "4096",
		{"recordsize", "1M"}:           "1048576",
		{"special_small_blocks", "0"}:  "0",
		{"mountpoint", "/srv/data"}:    "/srv/data",
		{"mountpoint", "legacy"}:       "legacy",
		{"com.example:owner", "alice"}: "alice",
		{"canmount", "noauto"}:         "noauto",
	}
	for in, want := range good {
		got, err := checkProperty(in[0], in[1])
		if err != nil || got != want {
			t.Errorf("checkProperty(%q, %q) = %q, %v; want %q", in[0], in[1], got, err, want)
		}
	}
	bad := [][2]string{
		{"compression", "zstd-20"},
		{"compression", "lz5"},
		{"quota", "-1"},
		{"quota", "1.5G"},
		{"quota", "lots"},
		{"recordsize", "1000"},
		{"recordsize", "32M"},
		{"mountpoint", "relative"},
		{"mountpoint", "/a/../b"},
		{"mountpoint", "-o"},
		{"atime", "yes"},
		{"guid", "1"},
		{"encryption", "on"},
		{"com.example:x", "-v"},
		{"com.example:x", "a\x1b[2J"},
		{"Com.Example:x", "a"},
	}
	for _, in := range bad {
		if got, err := checkProperty(in[0], in[1]); err == nil {
			t.Errorf("checkProperty(%q, %q) = %q, accepted", in[0], in[1], got)
		}
	}
}

func dsWorld() *fakeReader {
	r := world()
	r.props["tank/a"] = map[string]string{"compression": "lz4", "quota": "none", "mounted": "yes", "canmount": "on", "mountpoint": "/tank/a", "origin": "-"}
	r.props["tank"] = map[string]string{"compression": "off"}
	r.dry["tank/a"] = zfs.DestroyPreview{}
	r.dryErr = map[string]error{"tank/a": &execute.CommandError{ExitCode: 1, Stderr: termtext.New("cannot destroy 'tank/a': filesystem has children\n")}}
	r.dry["-r tank/a"] = zfs.DestroyPreview{Destroys: []string{"tank/a/x", "tank/a@2", "tank/a"}, Reclaim: 1 << 20}
	return r
}

func TestPropertySetTiersAndPreview(t *testing.T) {
	b := Builder{R: dsWorld()}
	p, err := b.PropertySet(t.Context(), "tank/a", "compression", "zstd")
	if err != nil {
		t.Fatal(err)
	}
	if p.Steps[0].String() != "zfs set -- compression=zstd tank/a" || mutate.TierOf(p) != mutate.Low {
		t.Errorf("step %q, tier %s", p.Steps[0].String(), mutate.TierOf(p))
	}
	got := p.Preview.Affects[0].Display() + " | " + p.Preview.Affects[1].Display()
	if !strings.Contains(got, "from lz4 to zstd") || !strings.Contains(got, "only data written afterwards") {
		t.Errorf("affects = %q", got)
	}
	if p.Baseline["tank/a compression"] != "lz4" {
		t.Errorf("baseline = %v", p.Baseline)
	}
	q, err := b.PropertySet(t.Context(), "tank/a", "quota", "1G")
	if err != nil || mutate.TierOf(q) != mutate.Medium || q.Steps[0].String() != "zfs set -- quota=1073741824 tank/a" {
		t.Errorf("quota: %+v, %v", q, err)
	}
}

func TestPropertyInheritShowsTheParentValue(t *testing.T) {
	p, err := Builder{R: dsWorld()}.PropertyInherit(t.Context(), "tank/a", "compression")
	if err != nil || !strings.Contains(p.Preview.Affects[0].Display(), "to off, the value of tank") {
		t.Fatalf("plan %+v, %v", p, err)
	}
}

func TestDatasetDestroy(t *testing.T) {
	b := Builder{R: dsWorld()}
	p, err := b.DatasetDestroy(t.Context(), "tank/a", false)
	if err != nil || len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "has children") {
		t.Fatalf("without -r: %q, %v", p.Blocked, err)
	}
	p, err = b.DatasetDestroy(t.Context(), "tank/a", true)
	if err != nil {
		t.Fatal(err)
	}
	// tank/a@2 is listed and has no holds or clones; mutate.TierOf
	// makes any dataset destroy High.
	if len(p.Blocked) != 0 || len(p.Preview.Destroys) != 3 || mutate.TierOf(p) != mutate.High {
		t.Fatalf("with -r: %+v", p)
	}
	if p, _ := b.DatasetDestroy(t.Context(), "tank", true); len(p.Blocked) == 0 {
		t.Error("destroying a pool's root dataset was not blocked")
	}
}

func TestCreateRenameMount(t *testing.T) {
	b := Builder{R: dsWorld()}
	p, err := b.DatasetCreate(t.Context(), "tank/new", map[string]string{"compression": "zstd", "quota": "", "mountpoint": "/srv/new"})
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zfs create -o compression=zstd -o mountpoint=/srv/new -- tank/new" {
		t.Fatalf("create: %+v, %v", p, err)
	}
	if _, err := b.DatasetCreate(t.Context(), "tank/new", map[string]string{"quota": "much"}); err == nil {
		t.Error("bad quota accepted")
	}
	if p, _ := b.DatasetCreate(t.Context(), "tank/a", nil); len(p.Blocked) != 1 {
		t.Errorf("create over an existing dataset: %q", p.Blocked)
	}
	r, err := b.DatasetRename(t.Context(), "tank/a", "tank/a/b")
	if err != nil || len(r.Blocked) == 0 || mutate.TierOf(r) != mutate.Medium {
		t.Errorf("rename below itself: %+v, %v", r, err)
	}
	m, err := b.Mount(t.Context(), "tank/a")
	if err != nil || len(m.Blocked) != 1 {
		t.Errorf("mount a mounted dataset: %q, %v", m.Blocked, err)
	}
	u, err := b.Unmount(t.Context(), "tank/a", true)
	if err != nil || len(u.Blocked) != 0 || mutate.TierOf(u) != mutate.High || u.Steps[0].String() != "zfs unmount -f -- tank/a" {
		t.Errorf("forced unmount: %+v, %v", u, err)
	}
}

func TestDatasetBuildersRebuildFromParams(t *testing.T) {
	b := Builder{R: dsWorld()}
	plans := []func() (*mutate.Plan, error){
		func() (*mutate.Plan, error) {
			return b.DatasetCreate(t.Context(), "tank/n", map[string]string{"compression": "lz4"})
		},
		func() (*mutate.Plan, error) { return b.DatasetRename(t.Context(), "tank/a", "tank/z") },
		func() (*mutate.Plan, error) { return b.DatasetDestroy(t.Context(), "tank/a", true) },
		func() (*mutate.Plan, error) { return b.PropertySet(t.Context(), "tank/a", "atime", "off") },
		func() (*mutate.Plan, error) { return b.PropertyInherit(t.Context(), "tank/a", "atime") },
		func() (*mutate.Plan, error) { return b.Unmount(t.Context(), "tank/a", false) },
	}
	for _, mk := range plans {
		p, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		q, err := b.Build(t.Context(), p.Op, p.Params)
		if err != nil || q.Steps[0].String() != p.Steps[0].String() {
			t.Errorf("%s rebuilt as %+v, %v", p.Op, q, err)
		}
	}
}

func TestUnprivilegedCreateChecksTheParent(t *testing.T) {
	b := Builder{R: dsWorld(), Unprivileged: true, Permits: func(_ context.Context, ds string) (map[string]bool, error) {
		if ds == "tank" {
			return map[string]bool{"create": true}, nil
		}
		return map[string]bool{"create": true, "mount": true}, nil
	}}
	p, err := b.DatasetCreate(t.Context(), "tank/new", nil)
	if err != nil || len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "delegated mount on tank") {
		t.Fatalf("create in tank without mount: %q, %v", p.Blocked, err)
	}
	if p.Steps[0].Done == "" {
		t.Error("an unprivileged create does not accept zfs's not-mounted message")
	}
	p, err = b.DatasetCreate(t.Context(), "tank/a/new", nil)
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("create in tank/a: %q, %v", p.Blocked, err)
	}
}

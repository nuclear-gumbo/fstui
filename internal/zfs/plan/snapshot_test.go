// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"slices"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func system() *fakeReader {
	return &fakeReader{
		ids: map[string]zfs.Identity{
			"tank/a": {GUID: "1", CreateTXG: "10"}, "tank/a/b": {GUID: "2", CreateTXG: "11"},
			"tank/a@old": {GUID: "3", CreateTXG: "12"},
		},
		kids: map[string][]string{"tank/a": {"tank/a/b"}},
	}
}

func TestSnapshotCreate(t *testing.T) {
	p, err := Builder{R: system()}.SnapshotCreate(t.Context(), []string{"tank/a"}, "new", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Steps[0].String(); got != "zfs snapshot -- tank/a@new" {
		t.Errorf("step = %q", got)
	}
	if p.Summary != "Create snapshot tank/a@new" || len(p.Blocked) != 0 || mutate.TierOf(p) != mutate.Low {
		t.Errorf("plan = %+v, tier %s", p, mutate.TierOf(p))
	}
	if p.Baseline["tank/a@new"] != mutate.Absent || p.Baseline["tank/a"] != "guid 1, createtxg 10" {
		t.Errorf("baseline = %v", p.Baseline)
	}
}

func TestSnapshotCreateRecursiveListsEverySnapshot(t *testing.T) {
	p, err := Builder{R: system()}.SnapshotCreate(t.Context(), []string{"tank/a"}, "new", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Steps[0].String(); got != "zfs snapshot -r -- tank/a@new" {
		t.Errorf("step = %q", got)
	}
	var affects []string
	for _, a := range p.Preview.Affects {
		affects = append(affects, a.Display())
	}
	if !slices.Equal(affects, []string{"creates tank/a@new", "creates tank/a/b@new"}) {
		t.Errorf("affects = %q", affects)
	}
	if !strings.Contains(p.Summary, "2 snapshots") {
		t.Errorf("summary = %q", p.Summary)
	}
}

func TestSnapshotCreateBlockedWhenTheNameExists(t *testing.T) {
	p, err := Builder{R: system()}.SnapshotCreate(t.Context(), []string{"tank/a"}, "old", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "already exists") {
		t.Errorf("blocked = %q", p.Blocked)
	}
}

func TestSnapshotCreateRefusesBadNames(t *testing.T) {
	for _, snap := range []string{"", "a@b", "a/b", "x\x1b[2J"} {
		if _, err := (Builder{R: system()}).SnapshotCreate(t.Context(), []string{"tank/a"}, snap, false); err == nil {
			t.Errorf("snapshot name %q accepted", snap)
		}
	}
}

func TestSnapshotCreateRecheckSeesAReplacedDataset(t *testing.T) {
	r := system()
	p, err := Builder{R: r}.SnapshotCreate(t.Context(), []string{"tank/a"}, "new", false)
	if err != nil {
		t.Fatal(err)
	}
	r.ids["tank/a"] = zfs.Identity{GUID: "99", CreateTXG: "50"}
	now, err := p.Recheck(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if d := p.Baseline.Diff(now); len(d) != 1 || !strings.HasPrefix(d[0], "tank/a changed") {
		t.Errorf("diff = %q", d)
	}
}

func TestBuildFromParamsMatches(t *testing.T) {
	b := Builder{R: system()}
	p, err := b.SnapshotCreate(t.Context(), []string{"tank/a"}, "new", true)
	if err != nil {
		t.Fatal(err)
	}
	q, err := b.Build(t.Context(), p.Op, p.Params)
	if err != nil {
		t.Fatal(err)
	}
	if q.Steps[0].String() != p.Steps[0].String() || q.Summary != p.Summary {
		t.Errorf("rebuilt %+v, want %+v", q, p)
	}
}

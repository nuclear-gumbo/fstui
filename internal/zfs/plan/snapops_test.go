// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"slices"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// world is tank/a with snapshots @1 (held, with clone tank/c) and @2,
// and bookmark #b after them; tank/c is a clone of tank/a@1.
func world() *fakeReader {
	id := func(g string) zfs.Identity { return zfs.Identity{GUID: g, CreateTXG: g} }
	return &fakeReader{
		ids: map[string]zfs.Identity{
			"tank": id("1"), "tank/a": id("2"), "tank/a@1": id("3"), "tank/a@2": id("4"), "tank/a#b": id("5"), "tank/c": id("6"),
		},
		props: map[string]map[string]string{
			"tank/a@1": {"userrefs": "1", "clones": "tank/c"},
			"tank/a@2": {"userrefs": "0", "clones": ""},
			"tank/c":   {"origin": "tank/a@1"},
			"tank/a":   {"origin": "-"},
		},
		holds: map[string][]string{"tank/a@1": {"keep"}},
		dry: map[string]zfs.DestroyPreview{
			"tank/a@2": {Destroys: []string{"tank/a@2"}, Reclaim: 4096, Output: "destroy\ttank/a@2\nreclaim\t4096\n"},
			"tank/a@1": {Destroys: []string{"tank/a@1"}, Reclaim: 0},
		},
		points: map[string][]zfs.Point{
			"tank/a": {{Name: "tank/a@1", CreateTXG: 3, Clones: []string{"tank/c"}}, {Name: "tank/a@2", CreateTXG: 4}, {Name: "tank/a#b", CreateTXG: 5}},
		},
	}
}

func TestDestroyOneSnapshot(t *testing.T) {
	p, err := Builder{R: world()}.SnapshotDestroy(t.Context(), []string{"tank/a@2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) != 0 || p.Preview.Frees != 4096 || mutate.TierOf(p) != mutate.Medium || p.Steps[0].String() != "zfs destroy -- tank/a@2" {
		t.Fatalf("plan = %+v, tier %s", p, mutate.TierOf(p))
	}
}

func TestDestroyBlockedByHoldsAndClones(t *testing.T) {
	p, err := Builder{R: world()}.SnapshotDestroy(t.Context(), []string{"tank/a@1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.Blocked, "\n")
	if !strings.Contains(got, "1 hold") || !strings.Contains(got, "clones (tank/c)") {
		t.Fatalf("blocked = %q", p.Blocked)
	}
}

func TestRollbackPastNewerNeedsChoiceAndStopsAtClones(t *testing.T) {
	b := Builder{R: world()}
	p, err := b.Rollback(t.Context(), "tank/a@2", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "1 newer snapshot or bookmark") {
		t.Fatalf("without destroy: blocked = %q", p.Blocked)
	}
	p, err = b.Rollback(t.Context(), "tank/a@2", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) != 0 || len(p.Preview.Destroys) != 1 || mutate.TierOf(p) != mutate.High || p.Steps[0].String() != "zfs rollback -r -- tank/a@2" {
		t.Fatalf("with destroy: %+v, tier %s", p, mutate.TierOf(p))
	}
	r := world()
	r.points["tank/a"][1].Clones = []string{"tank/x"}
	p, err = Builder{R: r}.Rollback(t.Context(), "tank/a@1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(p.Blocked, func(s string) bool { return strings.Contains(s, "clone tank/x") }) {
		t.Fatalf("past a clone: blocked = %q", p.Blocked)
	}
}

func TestRollbackToLatestIsMedium(t *testing.T) {
	r := world()
	r.points["tank/a"] = r.points["tank/a"][:2]
	p, err := Builder{R: r}.Rollback(t.Context(), "tank/a@2", false)
	if err != nil || len(p.Blocked) != 0 || mutate.TierOf(p) != mutate.Medium {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}

func TestHoldAndRelease(t *testing.T) {
	b := Builder{R: world()}
	p, err := b.Hold(t.Context(), []string{"tank/a@1"}, "keep", false)
	if err != nil || len(p.Blocked) != 1 {
		t.Fatalf("hold with an existing tag: %+v, %v", p, err)
	}
	p, err = b.Release(t.Context(), []string{"tank/a@1"}, "keep", false)
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zfs release -- keep tank/a@1" {
		t.Fatalf("release: %+v, %v", p, err)
	}
	if !strings.Contains(p.Preview.Affects[0].Display(), "can be destroyed afterwards") {
		t.Errorf("affects = %v", p.Preview.Affects)
	}
	for _, tag := range []string{"", "-r", "a\x1b[2J", "a\u202eb"} {
		if _, err := b.Hold(t.Context(), []string{"tank/a@1"}, tag, false); err == nil {
			t.Errorf("tag %q accepted", tag)
		}
	}
}

func TestRenameBookmarkCloneBlockedOnExistingNames(t *testing.T) {
	b := Builder{R: world()}
	if p, err := b.SnapshotRename(t.Context(), "tank/a@1", "2"); err != nil || len(p.Blocked) != 1 {
		t.Errorf("rename onto @2: %+v, %v", p, err)
	}
	if p, err := b.Bookmark(t.Context(), "tank/a@2", "b"); err != nil || len(p.Blocked) != 1 {
		t.Errorf("bookmark onto #b: %+v, %v", p, err)
	}
	if p, err := b.Clone(t.Context(), "tank/a@2", "tank/c"); err != nil || len(p.Blocked) != 1 {
		t.Errorf("clone onto tank/c: %+v, %v", p, err)
	}
	if p, err := b.Clone(t.Context(), "tank/a@2", "other/x"); err != nil || len(p.Blocked) < 1 {
		t.Errorf("clone into another pool: %+v, %v", p, err)
	}
	p, err := b.Clone(t.Context(), "tank/a@2", "tank/d")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zfs clone -- tank/a@2 tank/d" {
		t.Errorf("clone: %+v, %v", p, err)
	}
}

func TestPromote(t *testing.T) {
	b := Builder{R: world()}
	p, err := b.Promote(t.Context(), "tank/c")
	if err != nil || len(p.Blocked) != 0 || mutate.TierOf(p) != mutate.Medium {
		t.Fatalf("promote: %+v, %v", p, err)
	}
	if !strings.Contains(p.Preview.Affects[0].Display(), "moves tank/a@1 to tank/c@1") {
		t.Errorf("affects = %v", p.Preview.Affects)
	}
	if p, err := b.Promote(t.Context(), "tank/a"); err != nil || len(p.Blocked) != 1 {
		t.Errorf("promote a non-clone: %+v, %v", p, err)
	}
}

func TestEveryBuilderRebuildsFromParams(t *testing.T) {
	b := Builder{R: world()}
	plans := []func() (*mutate.Plan, error){
		func() (*mutate.Plan, error) { return b.Hold(t.Context(), []string{"tank/a@2"}, "t", true) },
		func() (*mutate.Plan, error) { return b.Release(t.Context(), []string{"tank/a@1"}, "keep", false) },
		func() (*mutate.Plan, error) { return b.SnapshotRename(t.Context(), "tank/a@2", "3") },
		func() (*mutate.Plan, error) { return b.Bookmark(t.Context(), "tank/a@2", "c") },
		func() (*mutate.Plan, error) { return b.Clone(t.Context(), "tank/a@2", "tank/d") },
		func() (*mutate.Plan, error) { return b.SnapshotDestroy(t.Context(), []string{"tank/a@2"}, false) },
		func() (*mutate.Plan, error) { return b.Rollback(t.Context(), "tank/a@2", true) },
		func() (*mutate.Plan, error) { return b.Promote(t.Context(), "tank/c") },
	}
	for _, mk := range plans {
		p, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		q, err := b.Build(t.Context(), p.Op, p.Params)
		if err != nil {
			t.Fatalf("%s: %v", p.Op, err)
		}
		if q.Summary != p.Summary || len(q.Steps) != len(p.Steps) || q.Steps[0].String() != p.Steps[0].String() {
			t.Errorf("%s rebuilt as %+v", p.Op, q)
		}
	}
}

func TestDestroyBlockedWhenTheDryRunRefuses(t *testing.T) {
	r := world()
	r.dryErr = map[string]error{"-r tank/a@2": &execute.CommandError{
		Argv: execute.Argv{Tool: execute.ZFS}, ExitCode: 1,
		Stderr: termtext.New("cannot destroy 'tank/a/x@2': snapshot has dependent clones\nuse '-R' to destroy\n"),
	}}
	p, err := Builder{R: r}.SnapshotDestroy(t.Context(), []string{"tank/a@2"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "dependent clones") || p.Preview.Frees != -1 {
		t.Fatalf("blocked = %q, frees %d", p.Blocked, p.Preview.Frees)
	}
}

func TestDestroySeveralAsksForWhatItDoes(t *testing.T) {
	r := world()
	r.dry["tank/a@1"] = zfs.DestroyPreview{Destroys: []string{"tank/a@1"}, Reclaim: 1}
	r.props["tank/a@1"] = map[string]string{"userrefs": "0", "clones": ""}
	p, err := Builder{R: r}.SnapshotDestroy(t.Context(), []string{"tank/a@1", "tank/a@2"}, false)
	if err != nil || p.TypeToConfirm != "destroy 2 snapshots" || mutate.TierOf(p) != mutate.High {
		t.Fatalf("plan %+v, %v", p, err)
	}
}

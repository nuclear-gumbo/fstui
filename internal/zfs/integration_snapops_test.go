// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration && !fstui_nowrite

package zfs_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

// mustRun builds a plan, checks it is not blocked, and runs it.
func mustRun(t *testing.T, pl *mutate.Pipeline, p *mutate.Plan, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) > 0 {
		t.Fatalf("%s blocked: %q", p.Summary, p.Blocked)
	}
	if rep, err := pl.Run(t.Context(), p); err != nil {
		t.Fatalf("%s: %+v, %v", p.Summary, rep, err)
	}
}

func TestIntegrationHoldReleaseDestroy(t *testing.T) {
	p := testpool.New(t, "", 1)
	p.CreateFilesystem("a")
	snap := p.Snapshot("a", "s1")
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()

	h, err := b.Hold(ctx, []string{snap}, "keep", false)
	mustRun(t, pl, h, err)
	d, err := b.SnapshotDestroy(ctx, []string{snap}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocked) != 1 || !strings.Contains(d.Blocked[0], "1 hold") || len(d.Preview.Destroys) != 1 {
		t.Fatalf("destroy of a held snapshot: blocked %q, destroys %v", d.Blocked, d.Preview.Destroys)
	}
	rel, err := b.Release(ctx, []string{snap}, "keep", false)
	mustRun(t, pl, rel, err)
	d, err = b.SnapshotDestroy(ctx, []string{snap}, false)
	mustRun(t, pl, d, err)
	if d.Preview.Frees < 0 {
		t.Errorf("no reclaim from zfs destroy -nvp: %q", d.Preview.Output.Raw())
	}
	ids, err := b.R.Identify(ctx, []string{snap})
	if err != nil || len(ids) != 0 {
		t.Fatalf("snapshot still there: %v %v", ids, err)
	}
}

func TestIntegrationRenameBookmarkClone(t *testing.T) {
	p := testpool.New(t, "", 1)
	fs := p.CreateFilesystem("a")
	p.CreateFilesystem("a/b")
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	c, err := b.SnapshotCreate(ctx, []string{fs}, "s1", true)
	mustRun(t, pl, c, err)
	r, err := b.SnapshotRename(ctx, fs+"@s1", "s2")
	mustRun(t, pl, r, err)
	bm, err := b.Bookmark(ctx, fs+"@s2", "m")
	mustRun(t, pl, bm, err)
	cl, err := b.Clone(ctx, fs+"/b@s1", p.Name+"/clone")
	mustRun(t, pl, cl, err)
	ids, err := b.R.Identify(ctx, []string{fs + "@s2", fs + "#m", p.Name + "/clone"})
	if err != nil || len(ids) != 3 {
		t.Fatalf("after rename, bookmark, clone: %v %v", ids, err)
	}
	// a/b@s1 now has a clone, which blocks destroying it.
	d, err := b.SnapshotDestroy(ctx, []string{fs + "/b@s1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(d.Blocked, func(s string) bool { return strings.Contains(s, "clones") }) {
		t.Fatalf("destroy of a cloned snapshot: blocked %q", d.Blocked)
	}
}

func TestIntegrationRollback(t *testing.T) {
	p := testpool.New(t, "", 1)
	p.CreateFilesystem("a")
	s1 := p.Snapshot("a", "s1")
	p.Snapshot("a", "s2")
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	rb, err := b.Rollback(ctx, s1, false)
	if err != nil || len(rb.Blocked) != 1 {
		t.Fatalf("rollback past s2 without -r: %q, %v", rb.Blocked, err)
	}
	rb, err = b.Rollback(ctx, s1, true)
	if err != nil || len(rb.Preview.Destroys) != 1 || mutate.TierOf(rb) != mutate.High {
		t.Fatalf("rollback -r: %+v, %v", rb, err)
	}
	mustRun(t, pl, rb, err)
	pts, err := b.R.PointsOf(ctx, p.Name+"/a")
	if err != nil || len(pts) != 1 || pts[0].Name != s1 {
		t.Fatalf("after rollback: %+v, %v", pts, err)
	}
}

func TestIntegrationPromote(t *testing.T) {
	p := testpool.New(t, "", 1)
	p.CreateFilesystem("a")
	s1 := p.Snapshot("a", "s1")
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	cl, err := b.Clone(ctx, s1, p.Name+"/c")
	mustRun(t, pl, cl, err)
	pr, err := b.Promote(ctx, p.Name+"/c")
	if err != nil || !strings.Contains(pr.Preview.Affects[0].Display(), "moves "+s1) {
		t.Fatalf("promote preview: %+v, %v", pr, err)
	}
	mustRun(t, pl, pr, err)
	facts, err := b.R.Get(ctx, []string{p.Name + "/a"}, []string{"origin"})
	if err != nil || facts[p.Name+"/a"]["origin"] != p.Name+"/c@s1" {
		t.Fatalf("after promote: %v, %v", facts, err)
	}
}

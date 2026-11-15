// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration && !fstui_nowrite

// The write integration tests are an external test package, so they can
// use internal/zfs/plan (which imports zfs) and still build into the one
// integration binary.
package zfs_test

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/audit"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// writeSetup returns a plan builder and a pipeline in write mode whose
// audit log is in a temporary directory.
func writeSetup(t *testing.T) (plan.Builder, *mutate.Pipeline, *execute.Switch, string) {
	t.Helper()
	tools, err := execute.Resolve(execute.ZFS, execute.Zpool)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "audit")
	log, err := audit.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	mode := new(execute.Switch)
	mode.Set(execute.Write)
	b := plan.Builder{R: zfs.NewReader(execute.NewRunner(tools))}
	return b, &mutate.Pipeline{Mode: mode, Mutator: execute.NewMutator(tools, mode, log)}, mode, filepath.Join(dir, audit.DefaultFile)
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	n := 0
	for sc := bufio.NewScanner(f); sc.Scan(); {
		n++
	}
	return n
}

func TestIntegrationIdentifyLeavesOutMissingNames(t *testing.T) {
	p := testpool.New(t, "", 1)
	fs := p.CreateFilesystem("a")
	snap := p.Snapshot("a", "s")
	tools, err := execute.Resolve(execute.ZFS, execute.Zpool)
	if err != nil {
		t.Fatal(err)
	}
	r := zfs.NewReader(execute.NewRunner(tools))
	ids, err := r.Identify(t.Context(), []string{fs, snap, fs + "@missing", p.Name + "/missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[fs].GUID == "" || ids[snap].CreateTXG == "" {
		t.Fatalf("Identify = %+v", ids)
	}
}

func TestIntegrationSnapshotCreate(t *testing.T) {
	p := testpool.New(t, "", 1)
	fs := p.CreateFilesystem("a")
	p.CreateFilesystem("a/b")
	b, pl, _, auditFile := writeSetup(t)

	pn, err := b.SnapshotCreate(t.Context(), []string{fs}, "s1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(pn.Preview.Affects) != 2 || len(pn.Blocked) != 0 || mutate.TierOf(pn) != mutate.Low {
		t.Fatalf("plan = %+v", pn)
	}
	rep, err := pl.Run(t.Context(), pn)
	if err != nil || len(rep.Ran) != 1 {
		t.Fatalf("Run = %+v, %v", rep, err)
	}
	again, err := b.SnapshotCreate(t.Context(), []string{fs}, "s1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Blocked) != 2 {
		t.Fatalf("second plan for the same names: blocked = %q", again.Blocked)
	}
	if got := countLines(t, auditFile); got != 2 {
		t.Fatalf("audit log has %d records, want attempt and result", got)
	}
}

func TestIntegrationSnapshotCreateRefusesAStalePlan(t *testing.T) {
	p := testpool.New(t, "", 1)
	fs := p.CreateFilesystem("a")
	b, pl, _, auditFile := writeSetup(t)
	pn, err := b.SnapshotCreate(t.Context(), []string{fs}, "s2", false)
	if err != nil {
		t.Fatal(err)
	}
	p.Snapshot("a", "s2") // someone else took it after the preview
	_, err = pl.Run(t.Context(), pn)
	var changed *mutate.StateChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("Run = %v, want StateChangedError", err)
	}
	if got := countLines(t, auditFile); got != 0 {
		t.Fatalf("audit log has %d records for a plan that did not run", got)
	}
}

func TestIntegrationSnapshotCreateReadOnlyAndDryRun(t *testing.T) {
	p := testpool.New(t, "", 1)
	fs := p.CreateFilesystem("a")
	b, pl, mode, auditFile := writeSetup(t)
	pn, err := b.SnapshotCreate(t.Context(), []string{fs}, "s3", false)
	if err != nil {
		t.Fatal(err)
	}
	mode.Set(execute.ReadOnly)
	if _, err := pl.Run(t.Context(), pn); !errors.Is(err, mutate.ErrReadOnly) {
		t.Fatalf("read-only Run = %v", err)
	}
	mode.Set(execute.DryRun)
	if rep, err := pl.Run(t.Context(), pn); err != nil || !rep.DryRun {
		t.Fatalf("dry run = %+v, %v", rep, err)
	}
	ids, err := b.R.Identify(t.Context(), []string{fs + "@s3"})
	if err != nil || len(ids) != 0 {
		t.Fatalf("dry run created the snapshot: %v, %v", ids, err)
	}
	if got := countLines(t, auditFile); got != 2 {
		t.Fatalf("dry run left %d audit records, want 2", got)
	}
}

func TestIntegrationSavedPlanAppliesOnce(t *testing.T) {
	p := testpool.New(t, "", 1)
	fs := p.CreateFilesystem("a")
	b, pl, _, _ := writeSetup(t)
	store := mutate.Store{Dir: filepath.Join(t.TempDir(), "plans"), Host: "test", Version: "test", Now: time.Now}
	pn, err := b.SnapshotCreate(t.Context(), []string{fs}, "saved", false)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := store.Save(pn)
	if err != nil {
		t.Fatal(err)
	}
	sv, err := store.Load(sha)
	if err != nil {
		t.Fatal(err)
	}
	again, err := mutate.Reapply(t.Context(), sha, sv, b.Build)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Run(t.Context(), again); err != nil {
		t.Fatal(err)
	}
	var changed *mutate.StateChangedError
	if _, err := mutate.Reapply(t.Context(), sha, sv, b.Build); !errors.As(err, &changed) {
		t.Fatalf("second Reapply = %v, want StateChangedError", err)
	}
}

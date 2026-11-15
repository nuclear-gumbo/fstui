// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package mutate

import (
	"context"
	"errors"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

func TestEveryOpHasATableRow(t *testing.T) {
	names := map[string]Op{}
	for o := Op(1); o < opEnd; o++ {
		r, ok := table[o]
		if !ok {
			t.Errorf("Op %d has no row in the tier table", o)
			continue
		}
		if r.base < Low || r.base > High || r.name == "" {
			t.Errorf("Op %d: bad row %+v", o, r)
		}
		if prev, dup := names[r.name]; dup {
			t.Errorf("Op %d and %d share the name %q", prev, o, r.name)
		}
		names[r.name] = o
		if back, ok := OpNamed(r.name); !ok || back != o {
			t.Errorf("OpNamed(%q) = %d, %v", r.name, back, ok)
		}
	}
	if len(table) != int(opEnd)-1 {
		t.Errorf("table has %d rows for %d operations", len(table), int(opEnd)-1)
	}
}

func TestTierOf(t *testing.T) {
	destroyed := []termtext.Text{termtext.New("tank/a@2")}
	tests := []struct {
		name string
		plan Plan
		want Tier
	}{
		{"snapshot", Plan{Op: SnapshotCreate, Targets: []string{"tank/a"}}, Low},
		{"snapshot of two datasets", Plan{Op: SnapshotCreate, Targets: []string{"tank/a", "tank/b"}}, Medium},
		{"destroy one snapshot", Plan{Op: SnapshotDestroy, Targets: []string{"tank/a@1"}}, Medium},
		{"destroy two snapshots", Plan{Op: SnapshotDestroy, Targets: []string{"tank/a@1", "tank/a@2"}}, High},
		{"destroy snapshot recursively", Plan{Op: SnapshotDestroy, Targets: []string{"tank/a@1"}, Flags: Flags{Recursive: true}}, High},
		{"destroy reaching outside", Plan{Op: SnapshotDestroy, Targets: []string{"tank/a@1"}, Preview: Preview{Outside: true}}, High},
		{"destroy dataset", Plan{Op: DatasetDestroy, Targets: []string{"tank/a"}}, High},
		{"rollback to latest", Plan{Op: Rollback, Targets: []string{"tank/a@2"}}, Medium},
		{"rollback destroying", Plan{Op: Rollback, Targets: []string{"tank/a@1"}, Preview: Preview{Destroys: destroyed}}, High},
		{"set compression", Plan{Op: PropertySet, Property: "compression"}, Low},
		{"set quota", Plan{Op: PropertySet, Property: "quota"}, Medium},
		{"inherit atime", Plan{Op: PropertyInherit, Property: "atime"}, Low},
		{"inherit mountpoint", Plan{Op: PropertyInherit, Property: "mountpoint"}, Medium},
		{"forced unmount", Plan{Op: Unmount, Flags: Flags{Force: true}}, High},
		{"enable write", Plan{Op: EnableWrite}, Medium},
		{"unknown op", Plan{Op: opEnd}, High},
	}
	for _, tt := range tests {
		if got := TierOf(&tt.plan); got != tt.want {
			t.Errorf("%s: TierOf = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestFingerprintDiff(t *testing.T) {
	was := Fingerprint{"tank/a": "guid 1", "tank/b": "guid 2"}
	now := Fingerprint{"tank/a": "guid 9", "tank/c": "guid 3"}
	got := was.Diff(now)
	want := []string{"tank/a changed (guid 9, was guid 1)", "tank/b no longer exists", "tank/c now exists"}
	if len(got) != len(want) {
		t.Fatalf("Diff = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Diff[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if d := (Fingerprint{"tank/a@x": Absent}).Diff(Fingerprint{"tank/a@x": "guid 5"}); len(d) != 1 || d[0] != "tank/a@x now exists (guid 5)" {
		t.Errorf("Diff of a name that appeared = %q", d)
	}
	if d := was.Diff(was); len(d) != 0 {
		t.Errorf("Diff of itself = %q", d)
	}
}

type nopRecorder struct{ n int }

func (r *nopRecorder) Record(execute.StepEvent) error { r.n++; return nil }

func pipeline(mode execute.Mode) (*Pipeline, *nopRecorder) {
	sw := new(execute.Switch)
	sw.Set(mode)
	rec := new(nopRecorder)
	tools, _ := execute.Resolve() // no tools: dry runs only
	return &Pipeline{Mode: sw, Mutator: execute.NewMutator(tools, sw, rec)}, rec
}

func step() []execute.Argv {
	return []execute.Argv{{Tool: execute.ZFS, Options: []string{"snapshot"}, Operands: []string{"tank/a@1"}}}
}

func TestRunRefusesInReadOnlyModeAndZeroesStdin(t *testing.T) {
	pl, rec := pipeline(execute.ReadOnly)
	secret := []byte("hunter2")
	p := &Plan{Op: SnapshotCreate, Steps: step(), Stdin: secret}
	if _, err := pl.Run(context.Background(), p); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Run = %v, want ErrReadOnly", err)
	}
	if rec.n != 0 || string(secret) != "\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("records %d, stdin %q", rec.n, secret)
	}
}

func TestRunRefusesBlockedPlans(t *testing.T) {
	pl, rec := pipeline(execute.DryRun)
	p := &Plan{Op: SnapshotDestroy, Steps: step(), Blocked: []string{"held by tag keep"}}
	if _, err := pl.Run(context.Background(), p); err == nil || rec.n != 0 {
		t.Fatalf("Run = %v, %d records", err, rec.n)
	}
}

func TestRunStopsWhenStateChanged(t *testing.T) {
	pl, rec := pipeline(execute.DryRun)
	p := &Plan{
		Op: SnapshotCreate, Steps: step(), Baseline: Fingerprint{"tank/a": "guid 1"},
		Recheck: func(context.Context) (Fingerprint, error) { return Fingerprint{"tank/a": "guid 2"}, nil },
	}
	_, err := pl.Run(context.Background(), p)
	var changed *StateChangedError
	if !errors.As(err, &changed) || rec.n != 0 {
		t.Fatalf("Run = %v, %d records", err, rec.n)
	}
}

func TestRunDryRun(t *testing.T) {
	pl, rec := pipeline(execute.DryRun)
	p := &Plan{
		Op: SnapshotCreate, Steps: step(), Baseline: Fingerprint{"tank/a": "guid 1"},
		Recheck: func(context.Context) (Fingerprint, error) { return Fingerprint{"tank/a": "guid 1"}, nil },
	}
	rep, err := pl.Run(context.Background(), p)
	if err != nil || !rep.DryRun || rec.n != 2 {
		t.Fatalf("Run = %+v, %v, %d records", rep, err, rec.n)
	}
}

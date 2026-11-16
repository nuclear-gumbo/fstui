// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package execute

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type recorder struct {
	events []StepEvent
	failAt int // fail the n-th Record call (from 1); 0 never
}

func (r *recorder) Record(e StepEvent) error {
	r.events = append(r.events, e)
	if len(r.events) == r.failAt {
		return errors.New("disk full")
	}
	return nil
}

func fakeMutator(t *testing.T, mode Mode, rec Recorder) *Mutator {
	t.Helper()
	sw := new(Switch)
	sw.Set(mode)
	return NewMutator(fakeRunner(t).tools, sw, rec)
}

func mark(t *testing.T) (Argv, string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "ran")
	return Argv{Tool: fake, Options: []string{"mark", f}}, f
}

func ran(f string) bool { _, err := os.Stat(f); return err == nil }

func TestMutateRefusesInReadOnlyMode(t *testing.T) {
	rec := new(recorder)
	a, f := mark(t)
	rep := fakeMutator(t, ReadOnly, rec).Mutate(PlanRef{ID: 1}, []Argv{a}, nil)
	if !errors.Is(rep.Err, ErrReadOnly) || len(rep.NotRun) != 1 || ran(f) || len(rec.events) != 0 {
		t.Fatalf("read-only Mutate = %+v, ran %v, %d records", rep, ran(f), len(rec.events))
	}
}

func TestMutateDryRunRecordsButRunsNothing(t *testing.T) {
	rec := new(recorder)
	a, f := mark(t)
	rep := fakeMutator(t, DryRun, rec).Mutate(PlanRef{ID: 7}, []Argv{a, a}, nil)
	if rep.Err != nil || !rep.DryRun || len(rep.Ran) != 2 || ran(f) {
		t.Fatalf("dry run = %+v, ran %v", rep, ran(f))
	}
	if len(rec.events) != 4 || rec.events[0].Mode != DryRun || rec.events[3].Step != 2 || rec.events[3].Phase != Outcome {
		t.Fatalf("records = %+v", rec.events)
	}
}

func TestMutateStopsAtFirstFailure(t *testing.T) {
	rec := new(recorder)
	a, f := mark(t)
	fail := Argv{Tool: fake, Options: []string{"fail", "x", "tank"}}
	rep := fakeMutator(t, Write, rec).Mutate(PlanRef{ID: 2}, []Argv{a, fail, a}, nil)
	if len(rep.Ran) != 1 || rep.Failed == nil || rep.Failed.ExitCode != 3 || len(rep.NotRun) != 1 || rep.Err == nil {
		t.Fatalf("report = %+v", rep)
	}
	if !ran(f) {
		t.Fatal("first step did not run")
	}
	last := rec.events[len(rec.events)-1]
	if len(rec.events) != 4 || last.ExitCode != 3 || last.Stderr.Display() == "" {
		t.Fatalf("records = %+v", rec.events)
	}
}

func TestMutateDoesNotRunAStepWhoseAttemptWasNotRecorded(t *testing.T) {
	rec := &recorder{failAt: 1}
	a, f := mark(t)
	rep := fakeMutator(t, Write, rec).Mutate(PlanRef{ID: 3}, []Argv{a}, nil)
	if rep.Err == nil || ran(f) || len(rep.NotRun) != 1 {
		t.Fatalf("report = %+v, ran %v", rep, ran(f))
	}
}

func TestMutateStopsWhenAResultCannotBeRecorded(t *testing.T) {
	rec := &recorder{failAt: 2}
	a, _ := mark(t)
	rep := fakeMutator(t, Write, rec).Mutate(PlanRef{ID: 4}, []Argv{a, a}, nil)
	if rep.Err == nil || len(rep.Ran) != 1 || len(rep.NotRun) != 1 {
		t.Fatalf("report = %+v", rep)
	}
}

// The passphrase reaches only the step marked as reading it; the steps
// after it (mounts after load-key) get an empty standard input.
func TestMutatePassesStdinOnlyToItsStep(t *testing.T) {
	steps := []Argv{{Tool: fake, Options: []string{"stdin"}, Stdin: true}, {Tool: fake, Options: []string{"stdin"}}}
	rep := fakeMutator(t, Write, new(recorder)).Mutate(PlanRef{ID: 5}, steps, []byte("secret\n"))
	if rep.Err != nil || len(rep.Ran) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if got := string(rep.Ran[0].Stdout); got != "got secret\n" {
		t.Errorf("the marked step got %q", got)
	}
	if got := string(rep.Ran[1].Stdout); got != "got \n" {
		t.Errorf("an unmarked step got %q", got)
	}
}

func TestMutateRefusesADashOperandWithoutSeparator(t *testing.T) {
	a, f := mark(t)
	a.Operands, a.NoSeparator = []string{"-r"}, true
	rep := fakeMutator(t, Write, new(recorder)).Mutate(PlanRef{ID: 9}, []Argv{a}, nil)
	if !errors.Is(rep.Err, ErrOperandOption) || ran(f) {
		t.Fatalf("report = %+v, ran %v", rep, ran(f))
	}
}

func TestMutateAcceptsADoneMessage(t *testing.T) {
	a := Argv{Tool: fake, Options: []string{"made"}, Done: "may only be mounted by root"}
	rep := fakeMutator(t, Write, new(recorder)).Mutate(PlanRef{ID: 10}, []Argv{a}, nil)
	if rep.Err != nil || len(rep.Ran) != 1 || rep.Ran[0].ExitCode != 1 || rep.Ran[0].Stderr.Raw() == "" {
		t.Fatalf("report = %+v", rep)
	}
	a.Done = "something else"
	if rep := fakeMutator(t, Write, new(recorder)).Mutate(PlanRef{ID: 11}, []Argv{a}, nil); rep.Err == nil {
		t.Fatal("a different message was taken as done")
	}
}

// Unresolved names each missing tool once; a simulated mutator needs none.
func TestUnresolvedTools(t *testing.T) {
	m := fakeMutator(t, Write, new(recorder))
	got := m.Unresolved([]Argv{{Tool: fake}, {Tool: Tee}, {Tool: Mv}, {Tool: Tee}})
	if len(got) != 2 || got[0] != Tee || got[1] != Mv {
		t.Errorf("Unresolved = %v", got)
	}
	sim := NewSimulatedMutator(new(Switch), new(recorder), func(Argv, []byte) (Result, error) { return Result{}, nil })
	if got := sim.Unresolved([]Argv{{Tool: Tee}}); got != nil {
		t.Errorf("simulated Unresolved = %v", got)
	}
}

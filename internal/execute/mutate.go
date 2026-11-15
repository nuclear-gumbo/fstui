// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package execute

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Mode is whether fstui may change storage.
type Mode int32

// Modes. ReadOnly is the zero value, so a Switch nobody set is read-only.
const (
	ReadOnly Mode = iota
	Write
	DryRun // plans are built and audited, nothing runs
)

func (m Mode) String() string {
	switch m {
	case Write:
		return "write"
	case DryRun:
		return "dry-run"
	case ReadOnly:
		return "read-only"
	}
	return "unknown"
}

// Switch holds the session's mode. The UI changes it (the W key) and the
// Mutator reads it before every plan, so a view cannot run a change by
// passing a mode of its own.
type Switch struct{ v atomic.Int32 }

// Set changes the mode.
func (s *Switch) Set(m Mode) { s.v.Store(int32(m)) }

// Get returns the mode.
func (s *Switch) Get() Mode { return Mode(s.v.Load()) }

// ErrReadOnly is returned by Mutate in read-only mode.
var ErrReadOnly = errors.New("fstui is in read-only mode; nothing was run")

// Phase of an audit record.
type Phase string

// Phases.
const (
	Attempt Phase = "attempt"
	Outcome Phase = "result"
)

// PlanRef names the plan a step belongs to in the audit log.
type PlanRef struct {
	ID     uint64
	Op     string // e.g. "snapshot-create"
	SHA256 string // of the saved plan it was applied from; empty otherwise
}

// StepEvent is what the Recorder is told before and after each step.
type StepEvent struct {
	Mode     Mode
	Plan     PlanRef
	Step     int // from 1
	Phase    Phase
	Argv     Argv
	ExitCode int           // result only
	Duration time.Duration // result only
	Stderr   termtext.Text // result only
}

// Recorder keeps the audit trail. A step runs only after its attempt
// was recorded; an error from Record stops the plan.
type Recorder interface {
	Record(StepEvent) error
}

// StepResult is one step that ran (or, in dry-run mode, would have).
type StepResult struct {
	Argv Argv
	Result
}

// Report is what Mutate did: the steps that ran, the one that failed,
// and the steps that did not run because of it.
type Report struct {
	Ran    []StepResult
	Failed *StepResult // nil if no step failed
	NotRun []Argv
	Err    error // why the plan stopped; nil when every step succeeded
	DryRun bool  // nothing was started
	// Simulated: the steps were carried out by the demo's simulation.
	Simulated bool
}

// Mutator runs changes. It is separate from Runner so that code holding
// only a Runner cannot change storage.
type Mutator struct {
	tools *Tools
	mode  *Switch
	audit Recorder
	// simulate, if set, carries out a step instead of starting a
	// process: the demo build changes its recorded state with it.
	simulate func(Argv, []byte) (Result, error)
}

// NewSimulatedMutator returns a Mutator that never starts a process: each
// step is handed to simulate, after the same mode checks and audit
// records as a real one. Only the demo build uses it.
func NewSimulatedMutator(mode *Switch, audit Recorder, simulate func(Argv, []byte) (Result, error)) *Mutator {
	return &Mutator{tools: &Tools{}, mode: mode, audit: audit, simulate: simulate}
}

// NewMutator returns a Mutator for tools resolved by Resolve, governed by
// mode and recording every step to audit.
func NewMutator(tools *Tools, mode *Switch, audit Recorder) *Mutator {
	return &Mutator{tools: tools, mode: mode, audit: audit}
}

// maxMutateOutput bounds what is kept of a step's output.
const maxMutateOutput = 1 << 20

// Mutate runs the steps of plan in order. It takes no context: a change that
// has started is never cancelled or timed out, and nothing is retried. The
// first failure stops the plan. stdin, if any, is given to every step; the
// caller zeroes it after return.
func (m *Mutator) Mutate(plan PlanRef, steps []Argv, stdin []byte) Report {
	mode := m.mode.Get()
	if mode == ReadOnly {
		return Report{NotRun: steps, Err: ErrReadOnly}
	}
	rep := Report{DryRun: mode == DryRun, Simulated: m.simulate != nil}
	for i, a := range steps {
		ev := StepEvent{Mode: mode, Plan: plan, Step: i + 1, Phase: Attempt, Argv: a}
		if err := m.audit.Record(ev); err != nil {
			rep.NotRun, rep.Err = steps[i:], fmt.Errorf("audit log: %w; nothing more was run", err)
			return rep
		}
		res, err := m.step(mode, a, stdin)
		ev.Phase, ev.ExitCode, ev.Duration, ev.Stderr = Outcome, res.ExitCode, res.Duration, res.Stderr
		auditErr := m.audit.Record(ev)
		sr := StepResult{Argv: a, Result: res}
		if err != nil {
			rep.Failed, rep.NotRun, rep.Err = &sr, steps[i+1:], err
			return rep
		}
		rep.Ran = append(rep.Ran, sr)
		if auditErr != nil {
			rep.NotRun, rep.Err = steps[i+1:], fmt.Errorf("audit log: %w; nothing more was run", auditErr)
			return rep
		}
	}
	return rep
}

func (m *Mutator) step(mode Mode, a Argv, stdin []byte) (Result, error) {
	if mode == DryRun {
		return Result{}, nil
	}
	if err := a.Check(); err != nil {
		return Result{ExitCode: -1}, err
	}
	if a.Input != nil {
		stdin = a.Input
	}
	if m.simulate != nil {
		return m.simulate(a, stdin)
	}
	path, ok := m.tools.Path(a.Tool)
	if !ok {
		return Result{ExitCode: -1}, fmt.Errorf("%s: %w", a.Tool, ErrNotResolved)
	}
	// As in Read: a verified absolute path and an argv vector, no shell.
	// No context: see Mutate.
	cmd := exec.Command(path, a.Args()...) //nolint:gosec,noctx // see above
	cmd.Env = env
	cmd.Dir = "/"
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stdout := &limitedBuffer{max: maxMutateOutput}
	stderr := &limitedBuffer{max: maxMutateOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	start := time.Now()
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: termtext.New(stderr.String()), Duration: time.Since(start)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
		if a.Done != "" && strings.Contains(res.Stderr.Raw(), a.Done) {
			return res, nil // made; the message stays in the result
		}
		return res, &CommandError{Argv: a, ExitCode: res.ExitCode, Stderr: res.Stderr}
	default:
		res.ExitCode = -1
		return res, fmt.Errorf("starting %s: %w", a, err)
	}
}

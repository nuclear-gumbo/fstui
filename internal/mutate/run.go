// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package mutate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// ErrReadOnly is returned when a confirmation is asked for in read-only
// mode: the first of the two mode checks.
var ErrReadOnly = errors.New("fstui is read-only. To make this change, press esc, then W")

// StateChangedError is returned when the re-check finds the system no
// longer matches the preview; nothing has run.
type StateChangedError struct {
	Changes []string
	Since   string // "you looked" unless set, e.g. "the plan was saved"
}

func (e *StateChangedError) Error() string {
	since := e.Since
	if since == "" {
		since = "you looked"
	}
	return "Something changed since " + since + ", so nothing was done: " + strings.Join(e.Changes, "; ")
}

// Pipeline runs confirmed plans.
type Pipeline struct {
	Mode    *execute.Switch
	Mutator *execute.Mutator
}

// CurrentMode returns the session's mode.
func (pl *Pipeline) CurrentMode() execute.Mode { return pl.Mode.Get() }

// Confirmable reports whether p may be put to the user for confirmation:
// not in read-only mode, and not blocked.
func (pl *Pipeline) Confirmable(p *Plan) error {
	// What makes the change impossible comes first: switching to write
	// mode would not help.
	if len(p.Blocked) > 0 {
		return fmt.Errorf("blocked: %s", strings.Join(p.Blocked, "; "))
	}
	if pl.Mode.Get() == execute.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

// Run re-checks p and runs its steps. It returns a StateChangedError,
// with nothing run, if the state differs from p.Baseline. Stdin is
// zeroed before Run returns, whatever happened.
func (pl *Pipeline) Run(ctx context.Context, p *Plan) (execute.Report, error) {
	defer clear(p.Stdin)
	if err := pl.Confirmable(p); err != nil {
		return execute.Report{NotRun: p.Steps}, err
	}
	if p.Recheck != nil {
		now, err := p.Recheck(ctx)
		if err != nil {
			return execute.Report{NotRun: p.Steps}, fmt.Errorf("checking the state before running: %w; nothing was run", err)
		}
		if d := p.Baseline.Diff(now); len(d) > 0 {
			return execute.Report{NotRun: p.Steps}, &StateChangedError{Changes: d}
		}
	}
	rep := pl.Mutator.Mutate(execute.PlanRef{ID: p.ID, Op: p.Op.String(), SHA256: p.SavedSHA256}, p.Steps, p.Stdin)
	return rep, rep.Err
}

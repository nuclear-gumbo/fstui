// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Remote runs commands on one host through a Runner: the readers use it
// as they use a Runner, and its errors name the host and the command
// fstui meant, not ssh's command line.
type Remote struct {
	run  *Runner
	host Host
}

// NewRemote binds run to h. Probe must succeed before anything else is
// run: until then the host has no checked tool paths.
func NewRemote(run *Runner, h Host) *Remote { return &Remote{run: run, host: h} }

// Destination is the host's ssh destination.
func (r *Remote) Destination() string { return r.host.Destination }

// Probe checks the host (see ParseProbe) and keeps the tool paths it
// found, so that later commands run those.
func (r *Remote) Probe(ctx context.Context) (Probe, error) {
	a, err := r.host.ProbeArgv()
	if err != nil {
		return Probe{}, err
	}
	res, err := r.run.Read(ctx, a)
	if err != nil {
		return Probe{}, r.named(Argv{Tool: SSH}, err)
	}
	p, err := ParseProbe(res.Stdout)
	if err != nil {
		return Probe{}, fmt.Errorf("%s: %w", r.host.Destination, err)
	}
	r.host.Paths = p.Paths
	return p, nil
}

// Read runs a on the host, as Runner.Read runs it locally.
func (r *Remote) Read(ctx context.Context, a Argv) (Result, error) {
	w, err := r.host.Wrap(a)
	if err != nil {
		return Result{}, err
	}
	res, err := r.run.Read(ctx, w)
	return res, r.named(a, err)
}

// ReadTo runs a on the host, as Runner.ReadTo runs it locally.
func (r *Remote) ReadTo(ctx context.Context, a Argv, consume func(io.Reader) error) error {
	w, err := r.host.Wrap(a)
	if err != nil {
		return err
	}
	return r.named(a, r.run.ReadTo(ctx, w, consume))
}

// Stream runs a on the host, as Runner.Stream runs it locally.
func (r *Remote) Stream(ctx context.Context, a Argv, line func([]byte)) error {
	w, err := r.host.Wrap(a)
	if err != nil {
		return err
	}
	return r.named(a, r.run.Stream(ctx, w, line))
}

// named makes an error from ssh about a: a command error carries a and
// the host, so it reads "zfs list ... on HOST exited ..."; any other
// error says which command on which host it concerns.
func (r *Remote) named(a Argv, err error) error {
	if err == nil {
		return nil
	}
	var ce *CommandError
	if errors.As(err, &ce) {
		ce.Argv, ce.Host = a, r.host.Destination
		return ce
	}
	return fmt.Errorf("%s on %s: %w", a, r.host.Destination, err)
}

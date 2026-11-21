// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/schedule"

	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/actions"
)

// The write module: everything that changes storage. A build
// with the fstui_nowrite tag uses module_nowrite.go instead and contains
// none of it.

func registerWrite(r *registry.Registry) {
	r.Add(registry.Module{Name: "write", Kind: registry.Feature})
}

const writeUsage = `
fstui starts read-only. Changes are possible in write mode (--write, or W
inside fstui); each is previewed and confirmed first and recorded in an
audit log. --dry-run plans and records changes without running them.
`

type writeFlags struct{ write, dryRun *bool }

func addWriteFlags(fs *pflag.FlagSet) *writeFlags {
	return &writeFlags{
		write:  fs.Bool("write", false, "start in write mode (W switches inside fstui)"),
		dryRun: fs.Bool("dry-run", false, "confirmed changes are recorded in the audit log but not run"),
	}
}

func checkWriteFlags(wf *writeFlags) error {
	if *wf.write && *wf.dryRun {
		return errors.New("--write and --dry-run exclude each other")
	}
	return nil
}

// writeHooks is what the backends give the write module: the tools
// changes run with, the changes offered for a selection, and how to
// build a saved plan again.
type writeHooks struct {
	tools   *execute.Tools
	offers  func(ui.Target) (string, []actions.Offer)
	rebuild mutate.Rebuilder
	perms   func(ui.Target) actions.Permissions // nil as root
	// demo: changes are simulated by simulate, and write mode keeps its
	// files apart from a real installation's.
	simulate func(execute.Argv, []byte) (execute.Result, error)
}

// startWrite sets up write support in the mode the flags ask for. When
// that mode cannot be had, fstui starts read-only and says why.
// withSchedules adds the Schedules tab's changes to the backend's, when
// the backend runs the system's tools, or says why it did not.
func withSchedules(h writeHooks, r schedule.Reader) (writeHooks, string) {
	b := schedule.Builder{
		Reader: r, Binary: fstuiBinary, Unprivileged: os.Geteuid() != 0,
		Has: func(t execute.Tool) bool { _, ok := h.tools.Path(t); return ok },
	}
	switch {
	case h.simulate != nil: // the demo: its steps are carried out in its own system
		b.Binary = func() (string, error) { return "/usr/bin/fstui", nil }
		b.Unprivileged, b.Has = false, func(execute.Tool) bool { return true }
	case h.offers == nil || h.tools == nil:
		return h, "Changing schedules needs a storage backend, which this build leaves out"
	}
	offers, rebuild := h.offers, h.rebuild
	h.offers = actions.Either(actions.ScheduleOffers(b), offers)
	h.rebuild = func(ctx context.Context, op mutate.Op, params map[string]string) (*mutate.Plan, error) {
		if op == mutate.ScheduleSet {
			return b.Build(ctx, op, params)
		}
		return rebuild(ctx, op, params)
	}
	return h, ""
}

// fstuiBinary is this fstui, for a unit, cron line or script that runs
// it as root: only a file no one but root could replace.
func fstuiBinary() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("finding fstui's own path: %w", err)
	}
	return execute.Trusted(p) // names the file and the problem
}

func startWrite(wf *writeFlags, h writeHooks, auditEntries int) (ui.Actions, []ui.Screen, string, func(), []string) {
	if h.offers == nil {
		return nil, nil, "", func() {}, nil
	}
	paths, err := actions.DefaultPaths()
	if h.simulate != nil {
		paths, err = actions.DemoPaths()
	}
	if err != nil {
		return nil, nil, "", func() {}, []string{"No changes are possible: " + err.Error()}
	}
	host, _ := os.Hostname() // recorded in saved plans; empty if unknown
	s := actions.NewSession(actions.Config{
		Tools: h.tools, Paths: paths, Offers: h.offers, Rebuild: h.rebuild, Permissions: h.perms, Simulate: h.simulate,
		Host: host, Version: buildVersion(), Now: time.Now, AuditEntries: auditEntries,
	})
	var notices []string
	switch {
	case *wf.dryRun:
		if err := s.EnableDryRun(); err != nil {
			notices = append(notices, "Started read-only, not in dry-run mode: "+err.Error())
		}
	case *wf.write:
		if err := s.EnableWrite(); err != nil {
			notices = append(notices, "Started read-only, not in write mode: "+err.Error())
		}
	}
	return s, s.Screens(), s.ModeWord(), s.Close, notices
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/ui/keys"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/audit"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// Paths are where write mode keeps its lock and audit log. Root uses
// the system locations (/run/fstui.lock, /var/log/fstui); a user with delegated
// permissions uses their own runtime and state directories.
type Paths struct {
	Lock     string
	AuditDir string
	Plans    string // saved plans
	NoSyslog bool   // the demo: records go to the file only
}

// DemoPaths keeps the demo's lock, audit log and saved plans in a
// directory of its own under the temporary directory, so trying the
// demo never touches a real installation's files or syslog.
func DemoPaths() (Paths, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("fstui-demo-%d", os.Geteuid()))
	if err := mutate.PrivateDir(dir); err != nil {
		return Paths{}, fmt.Errorf("demo files: %w", err)
	}
	return Paths{
		Lock: filepath.Join(dir, "lock"), AuditDir: filepath.Join(dir, "audit"), Plans: filepath.Join(dir, "plans"), NoSyslog: true,
	}, nil
}

// DefaultPaths returns the paths for the effective user.
func DefaultPaths() (Paths, error) {
	if os.Geteuid() == 0 {
		return Paths{Lock: "/run/fstui.lock", AuditDir: audit.DefaultDir, Plans: "/var/lib/fstui/plans"}, nil
	}
	run := os.Getenv("XDG_RUNTIME_DIR")
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("no state directory for the audit log: %w", err)
		}
		state = filepath.Join(home, ".local", "state")
	}
	if run == "" {
		return Paths{}, errors.New("XDG_RUNTIME_DIR is not set, so there is nowhere for the write-mode lock")
	}
	return Paths{
		Lock: filepath.Join(run, "fstui.lock"), AuditDir: filepath.Join(state, "fstui"), Plans: filepath.Join(state, "fstui", "plans"),
	}, nil
}

// Session is the write module's state for one fstui process: the mode,
// the lock, the audit log and the pipeline. It implements ui.Actions.
type Session struct {
	mode     *execute.Switch
	pipeline *mutate.Pipeline
	paths    Paths
	offers   func(ui.Target) (string, []Offer)
	perms    func(ui.Target) Permissions
	rebuild  mutate.Rebuilder
	store    mutate.Store
	now      func() time.Time

	mu    sync.Mutex
	lock  *Lock
	audit *audit.Log
}

// Config is what a Session is made from.
type Config struct {
	Tools *execute.Tools
	Paths Paths
	// Offers lists the changes for a selection and a title for the menu.
	Offers func(ui.Target) (string, []Offer)
	// Rebuild builds a plan from a saved operation and parameters.
	Rebuild mutate.Rebuilder
	// Permissions reads the user's delegated permissions on a
	// selection; nil when fstui runs as root.
	Permissions func(ui.Target) Permissions
	Host        string // recorded in saved plans
	Version     string
	Now         func() time.Time
	// Simulate, if set, carries out changes instead of running zfs (the
	// demo build).
	Simulate func(execute.Argv, []byte) (execute.Result, error)
}

// NewSession returns a read-only session.
func NewSession(c Config) *Session {
	s := &Session{
		mode: new(execute.Switch), paths: c.Paths, offers: c.Offers, perms: c.Permissions, rebuild: c.Rebuild, now: c.Now,
		store: mutate.Store{Dir: c.Paths.Plans, Host: c.Host, Version: c.Version, Now: c.Now},
	}
	mut := execute.NewMutator(c.Tools, s.mode, recorder{s})
	if c.Simulate != nil {
		mut = execute.NewSimulatedMutator(s.mode, recorder{s}, c.Simulate)
	}
	s.pipeline = &mutate.Pipeline{Mode: s.mode, Mutator: mut}
	return s
}

// recorder forwards to the audit log once it is open; before that,
// every record fails, so nothing can run unaudited.
type recorder struct{ s *Session }

func (r recorder) Record(ev execute.StepEvent) error {
	r.s.mu.Lock()
	l := r.s.audit
	r.s.mu.Unlock()
	if l == nil {
		return errors.New("the audit log is not open")
	}
	return l.Record(ev) //nolint:wrapcheck // the audit package's errors say what failed
}

// Pipeline returns the pipeline plans run through.
func (s *Session) Pipeline() *mutate.Pipeline { return s.pipeline }

// ModeWord is the status bar's word for the current mode.
func (s *Session) ModeWord() string { return modeWord(s.mode.Get()) }

// openAudit opens the audit log once.
func (s *Session) openAudit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.audit != nil {
		return nil
	}
	l, err := audit.Open(s.paths.AuditDir)
	if err != nil {
		return fmt.Errorf("write mode needs the audit log: %w", err)
	}
	if s.paths.NoSyslog {
		l.WithoutSyslog()
	}
	s.audit = l
	return nil
}

// EnableWrite takes the instance lock and opens the audit log, then
// switches to write mode. On failure fstui stays read-only.
func (s *Session) EnableWrite() error {
	if s.mode.Get() == execute.Write {
		return nil
	}
	if err := s.openAudit(); err != nil {
		return err
	}
	l, err := TakeLock(s.paths.Lock)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.lock = l
	s.mu.Unlock()
	s.mode.Set(execute.Write)
	return nil
}

// EnableDryRun switches to dry-run mode, for --dry-run. No lock is
// needed: nothing runs.
func (s *Session) EnableDryRun() error {
	if err := s.openAudit(); err != nil {
		return err
	}
	s.mode.Set(execute.DryRun)
	return nil
}

// DisableWrite returns to read-only and releases the lock.
func (s *Session) DisableWrite() {
	s.mode.Set(execute.ReadOnly)
	s.mu.Lock()
	s.lock.Release()
	s.lock = nil
	s.mu.Unlock()
}

// Close releases the lock and closes the audit log.
func (s *Session) Close() {
	s.DisableWrite()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.audit != nil {
		_ = s.audit.Close()
	}
}

// ToggleWrite implements ui.Actions: leaving write mode is immediate;
// entering it is a medium-tier confirmation.
func (s *Session) ToggleWrite() tea.Cmd {
	switch s.mode.Get() {
	case execute.Write:
		s.DisableWrite()
		return modeCmd(s.ModeWord())
	case execute.DryRun:
		return open(NewMessage("dry run", "fstui was started with --dry-run, which lasts for the whole session. Restart without it for write mode."))
	case execute.ReadOnly:
	}
	p := &mutate.Plan{
		ID: mutate.NewID(), Op: mutate.EnableWrite, Summary: "Switch to write mode",
		Preview: mutate.Preview{Affects: []termtext.Text{
			termtext.New("fstui can make changes. Each change applies following confirmation."),
			termtext.New("Every change is written to " + filepath.Join(s.paths.AuditDir, audit.DefaultFile) + "."),
			termtext.New("Any other fstui stays read-only until you switch back with W."),
		}},
	}
	return open(NewConfirm(p, enabler{s}, nil, s.now))
}

// enabler is the Runner of the "switch to write mode" confirmation.
type enabler struct{ s *Session }

func (e enabler) Confirmable(*mutate.Plan) error { return nil }

func (e enabler) CurrentMode() execute.Mode { return e.s.mode.Get() }

func (e enabler) Run(context.Context, *mutate.Plan) (execute.Report, error) {
	return execute.Report{}, e.s.EnableWrite()
}

// Menu implements ui.Actions.
func (s *Session) Menu(t ui.Target) (ui.Modal, tea.Cmd) {
	title, offers := s.offers(t)
	if offers == nil {
		return nil, nil
	}
	var perms Permissions
	if s.perms != nil {
		perms = s.perms(t)
	}
	return NewMenu(title, offers, s.Confirm, perms)
}

// Snapshot implements ui.Actions: the n key opens the menu of the
// selection's dataset straight at "Create snapshot", so permissions are
// checked as for the menu.
func (s *Session) Snapshot(t ui.Target) (ui.Modal, tea.Cmd) {
	switch {
	case len(t.Names) == 0:
		return nil, nil
	case t.Kind == "snapshot":
		ds, _, _ := strings.Cut(t.Names[0], "@")
		t = ui.Target{Backend: t.Backend, Kind: "filesystem", Names: []string{ds}}
	case t.Kind == "pool": // the pool's root dataset
		t = ui.Target{Backend: t.Backend, Kind: "filesystem", Names: t.Names}
	case t.Kind == "device": // its pool's root dataset
		t = ui.Target{Backend: t.Backend, Kind: "filesystem", Names: []string{t.Parent}}
	}
	md, cmd := s.Menu(t)
	m, ok := md.(*Menu)
	if !ok {
		return nil, nil
	}
	return m.autoChoose("s", cmd)
}

// CanSnapshot implements ui.Actions: everything but the ARC belongs to
// a dataset that can have one.
func (s *Session) CanSnapshot(t ui.Target) bool { return t.Kind != "arc" && len(t.Names) > 0 }

// Offered implements ui.Actions.
func (s *Session) Offered(t ui.Target) []key.Binding {
	_, offers := s.offers(t)
	out := make([]key.Binding, 0, len(offers))
	for _, o := range offers {
		out = append(out, key.NewBinding(key.WithKeys(o.Key), key.WithHelp(o.Key, o.Name)))
	}
	return out
}

// NoChanges implements ui.Actions: why a has nothing to offer here.
func (s *Session) NoChanges(view string, empty bool) ui.Modal {
	act := keys.Get(keys.Actions).Help().Key
	if empty {
		return NewMessage("nothing selected", "Select an item in "+view+", then press "+act+".")
	}
	return NewMessage("no changes in "+strings.ToLower(view),
		view+" has nothing to change. Changes are in Pools (1), Datasets (2), Snapshots (3), Volumes (4) and ARC (5): select an item there and press "+act+".")
}

// Confirm opens the preview of p, run through the pipeline; the plan
// can be saved from there.
func (s *Session) Confirm(p *mutate.Plan, rebuild Rebuild) ui.Modal {
	c := NewConfirm(p, savedRunner{s}, rebuild, s.now)
	c.save = s.store.Save
	return c
}

// savedRunner runs plans through the pipeline and records what became
// of a plan applied from a saved one.
type savedRunner struct{ s *Session }

func (r savedRunner) CurrentMode() execute.Mode        { return r.s.pipeline.CurrentMode() }
func (r savedRunner) Confirmable(p *mutate.Plan) error { return r.s.pipeline.Confirmable(p) }

func (r savedRunner) Run(ctx context.Context, p *mutate.Plan) (execute.Report, error) {
	rep, err := r.s.pipeline.Run(ctx, p)
	if p.SavedSHA256 != "" && !rep.DryRun {
		state, reason := "applied", ""
		if err != nil {
			state, reason = "refused", err.Error()
			if len(rep.Ran) > 0 || rep.Failed != nil {
				state = "failed"
			}
		}
		_ = r.s.store.Mark(p.SavedSHA256, state, reason) // the audit log has the full record
	}
	return rep, err //nolint:wrapcheck // the pipeline's errors are shown as they are
}

// Screens returns the write module's views.
func (s *Session) Screens() []ui.Screen { return []ui.Screen{NewChanges(s)} }

func open(m ui.Modal) tea.Cmd { return func() tea.Msg { return ui.OpenModalMsg{Modal: m} } }

func modeCmd(word string) tea.Cmd { return func() tea.Msg { return ui.ModeMsg{Mode: word} } }

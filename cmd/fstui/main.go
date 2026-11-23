// SPDX-License-Identifier: GPL-3.0-or-later

// Command fstui is a terminal UI for inspecting and managing storage.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/schedule"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/notify"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "devel"

// buildVersion is version, or for a binary built with go install, the
// module version Go recorded in it.
func buildVersion() string {
	if version != "devel" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// Exit codes.
const (
	// exitFailed: the UI could not start or stopped with an error.
	exitFailed = 1
	// exitUsage: fstui cannot do anything useful (no backends compiled in,
	// bad flags, or no terminal).
	exitUsage = 2
	// exitWrite: writing to stdout or stderr failed, so the user could not
	// see the result; reported through the exit code since there is nowhere
	// else to report it.
	exitWrite = 3
)

// modules registers every module compiled into this build, in a fixed order.
// Each register function lives in a pair of files selected by its build tag.
func modules() *registry.Registry {
	r := new(registry.Registry)
	registerZFS(r)
	registerWrite(r)
	registerRemote(r)
	return r
}

func main() {
	if err := harden(); err != nil {
		fmt.Fprintf(os.Stderr, "fstui: %v\n", err)
		os.Exit(exitFailed)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usageHeader precedes the option list of --help.
const usageHeader = `Usage: fstui [options]
       fstui check [options]
       fstui watch [options]

A terminal UI for looking at ZFS pools, datasets, snapshots and volumes.
fstui check looks at every pool once and sends notifications; fstui watch
does so until stopped, for a service.
`

// opened is what the compiled-in backends opened: their views, and what
// they give the write module.
type opened struct {
	screens []ui.Screen
	write   writeHooks
	// notifyInfo adds the backend's lines to the Notifications tab;
	// notifyTest sends its test messages (notify.Test when nil).
	notifyInfo func() []string
	notifyTest ui.Tester
	// poolNames lists the pools, for the Alerts tab's hints.
	poolNames func() []string
	// scheduleTasks lists the backend's periodic work for the Schedules
	// tab. scheduleHost and scheduleRun replace this system and its
	// systemctl (the demo's own); nil reads the real ones.
	scheduleTasks func(context.Context) ([]schedule.Task, []schedule.Job, error)
	scheduleHost  *schedule.Host
	scheduleRun   schedule.Runner
	// cleanup runs when fstui exits.
	cleanup func()
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "check":
			return runCheck(args[1:], stdout, stderr)
		case "watch":
			return runWatch(args[1:], stdout, stderr)
		}
	}
	// GNU-style options: --long, and -x for the few that have a short form.
	fs := pflag.NewFlagSet("fstui", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.SortFlags = false
	showVersion := fs.BoolP("version", "V", false, "print the version, compiled-in modules and OpenZFS versions, then exit")
	listThemes := fs.Bool("list-themes", false, "list the themes fstui can use, then exit")
	superuser := fs.Bool("superuser", false, "hide advisory warnings, never confirmations (also superuser: true in config.yaml)")
	dashboard := fs.Bool("dashboard", false, "start on the dashboard (view 0), for a screen left on all day")
	host := addRemoteFlags(fs)
	wf := addWriteFlags(fs)
	fs.BoolP("help", "h", false, "print this help, then exit")
	fs.Usage = func() {}
	switch err := fs.Parse(args); {
	case errors.Is(err, pflag.ErrHelp):
		return report(stdout, 0, "%s%s\nOptions:\n%s", usageHeader, writeUsage, optionList(fs))
	case err != nil:
		return report(stderr, exitUsage, "fstui: %v\nTry 'fstui --help' for the options.\n", err)
	case fs.NArg() > 0:
		return report(stderr, exitUsage, "fstui: unexpected argument %q\nTry 'fstui --help' for the options.\n", fs.Arg(0))
	}
	if err := checkWriteFlags(wf); err != nil {
		return report(stderr, exitUsage, "fstui: %v\n", err)
	}
	if help, _ := fs.GetBool("help"); help {
		return report(stdout, 0, "%s%s\nOptions:\n%s", usageHeader, writeUsage, optionList(fs))
	}

	if *listThemes {
		return printThemes(stdout)
	}
	mods := modules()
	if *showVersion {
		return printVersion(stdout, mods)
	}
	if !mods.HasBackend() {
		return report(stderr, exitUsage, "This build of fstui includes no storage backends. Rebuild without the fstui_no* tags for the filesystems you need.\n")
	}
	if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
		return report(stderr, exitUsage, "fstui is interactive and needs a terminal; fstui --version works without one.\n")
	}
	// Choosing another host in the host list ends the program; it starts
	// again on that host, so every view is built for it as at start-up.
	return runHosts(stderr, *superuser, *dashboard, ui.HostEntry{Name: *host}, wf)
}

// optionList formats the options as GNU tools do. pflag's own list marks
// every on/off switch with [=true|false], which says nothing useful here.
func optionList(fs *pflag.FlagSet) string {
	var b strings.Builder
	fs.VisitAll(func(f *pflag.Flag) {
		short := "    "
		if f.Shorthand != "" {
			short = "-" + f.Shorthand + ", "
		}
		fmt.Fprintf(&b, "  %s--%-12s %s\n", short, f.Name, f.Usage)
	})
	return b.String()
}

// start loads the config, opens every compiled-in backend and runs the UI.
// runHosts runs the interface on choice, and again on each host chosen
// in the host list.
func runHosts(stderr io.Writer, superuser, dashboard bool, choice ui.HostEntry, wf *writeFlags) int {
	// A host that cannot be opened after a switch leaves fstui on the one
	// it came from, saying why.
	var carried []string
	var previous *ui.HostEntry
	for {
		code, next, openErr := start(stderr, superuser, dashboard, choice, wf, carried)
		carried = nil
		switch {
		case openErr != nil && previous != nil:
			first, _, _ := strings.Cut(openErr.Error(), "\n")
			carried = []string{fmt.Sprintf("Could not show %s: %s", choice.Name, first)}
			choice, previous = *previous, nil
		case openErr != nil:
			return report(stderr, exitFailed, "fstui: %v\n", openErr)
		case next == nil:
			return code
		default:
			from := choice
			choice, previous = *next, &from
		}
	}
}

// start runs the interface on the chosen host. It returns the next
// host chosen in the host list, if any, or why the host could not be
// opened (the caller reports it or goes back).
func start(stderr io.Writer, superuserFlag, dashboard bool, choice ui.HostEntry, wf *writeFlags, carried []string) (int, *ui.HostEntry, error) {
	notices := carried
	user, err := config.UserFile()
	if err != nil {
		notices = append(notices, err.Error()+"; settings cannot be saved this session")
		user = ""
	}
	files := config.Files(user)
	cfg, problems := config.Load(files...)
	for _, p := range problems {
		notices = append(notices, p.String())
	}
	if superuserFlag {
		cfg.Superuser = true
		cfg.Sources["superuser"] = config.FlagSource
	}
	notices = append(notices, keys.Configure(cfg.Keys)...)
	th, themeProblems := config.LoadTheme(cfg.Theme, config.ThemeDirs()...)
	for _, p := range themeProblems {
		notices = append(notices, p.String())
	}

	notices = append(notices, hostNotices(cfg, choice.Name, wf)...)
	backends, host, priv, err := openBackends(cfg, choice)
	if err != nil {
		return 0, nil, withNotices(err, notices)
	}
	if backends.cleanup != nil {
		defer backends.cleanup()
	}
	schedules := newSchedules(files, cfg, &backends)
	actions, writeScreens, mode, closeWrite, writeNotices := startWrite(wf, backends.write, cfg.Limits.AuditEntries)
	defer closeWrite()
	notices = append(notices, writeNotices...)
	general := ui.NewSettings(user, files, cfg, config.ThemeNames(config.ThemeDirs()...), config.Load,
		func(name string) (theme.Theme, []config.Problem) {
			return config.LoadTheme(name, config.ThemeDirs()...)
		})
	tester := backends.notifyTest
	if tester == nil {
		tester = notify.Test
	}
	secretsFiles := config.SecretsFiles(user)
	notifications := ui.NewNotifications(user, cfg, func() (config.Secrets, []config.Problem) { return config.LoadSecrets(secretsFiles...) }, backends.notifyInfo, tester)
	alerts := ui.NewAlerts(user, cfg, backends.poolNames)
	screens := append(append(backends.screens, writeScreens...), configView(ui.NewConfig(general, notifications, alerts, schedules), user, cfg))

	session := ui.Session{Host: termtext.New(host), Privilege: priv, Label: buildLabel, Mode: mode, Superuser: cfg.Superuser, Notices: notices}
	profile := colorProfile()
	m := ui.New(session, screens, ui.Options{
		Theme: th, Profile: profile, Overrides: cfg.Colors, Exact: cfg.Units == config.Exact,
		ASCII: cfg.UseASCII(os.Getenv("TERM")), Mouse: cfg.Mouse, Natural: cfg.NaturalScrolling, Now: time.Now, StartView: startView(dashboard, cfg.UI.StartView), NoBlink: !cfg.UI.CursorBlink,
		Actions: actions, Hosts: hostLister(choice.Name), Config: cfg,
	})
	next, err := runProgram(m, profile)
	if err != nil {
		return report(stderr, exitFailed, "fstui: %v\n", err), nil, nil
	}
	return 0, next, nil
}

// runProgram runs the interface until it ends, and returns the host the
// user chose to see next, if that is why it ended.
func runProgram(m ui.Model, profile colorprofile.Profile) (*ui.HostEntry, error) {
	final, err := tea.NewProgram(m, tea.WithColorProfile(profile)).Run()
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller prefixes fstui
	}
	if fm, ok := final.(ui.Model); ok {
		if next, ok := fm.Next(); ok {
			return &next, nil
		}
	}
	return nil, nil
}

// withNotices adds what the status bar would have said to an error that
// ends fstui before it starts: it may be the reason, such as a config
// file ignored because others can write it.
func withNotices(err error, notices []string) error {
	if len(notices) == 0 {
		return err
	}
	return fmt.Errorf("%w\n%s", err, strings.TrimSuffix(noticeLines(notices), "\n"))
}

// noticeLines lists notices, one per line, for stderr.
func noticeLines(notices []string) string {
	var b strings.Builder
	for _, n := range notices {
		b.WriteString("fstui: " + n + "\n")
	}
	return b.String()
}

// hostNotices are what the status bar says about hosts at the start.
func hostNotices(cfg config.Config, hostName string, wf *writeFlags) []string {
	var out []string
	if n := hostsNotice(cfg); n != "" {
		out = append(out, n)
	}
	if hostName != "" && wantsChanges(wf) {
		out = append(out, "Started read-only: changes on other hosts come in a later release")
	}
	return out
}

// openBackends opens this machine's backends, or the chosen host, with
// the status bar's host and privilege fields.
func openBackends(cfg config.Config, choice ui.HostEntry) (opened, string, string, error) {
	if choice.Name != "" && choice.Name != config.LocalHost {
		h, err := openHost(cfg, choice)
		return h.opened, h.name, h.privilege, err
	}
	host, _ := os.Hostname() // empty is shown as empty; nothing depends on it
	o, err := zfsScreens(cfg)
	return o, host, privilege(), err
}

// printThemes lists shipped themes and theme files in the theme
// directories. A file there shadows a shipped theme of the same name.
func printThemes(w io.Writer) int {
	if code := report(w, 0, "shipped: %s\n", strings.Join(theme.BuiltinNames(), ", ")); code != 0 {
		return code
	}
	for _, dir := range config.ThemeDirs() {
		files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil || len(files) == 0 {
			continue
		}
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = termtext.New(strings.TrimSuffix(filepath.Base(f), ".yaml")).Display()
		}
		if code := report(w, 0, "%s: %s\n", dir, strings.Join(names, ", ")); code != 0 {
			return code
		}
	}
	return 0
}

// privilege is the status bar's privilege field.
func privilege() string {
	if os.Geteuid() == 0 {
		return "root"
	}
	if u, err := user.Current(); err == nil {
		return "unprivileged (" + termtext.New(u.Username).Display() + ")"
	}
	return "unprivileged"
}

// colorProfile honours NO_COLOR with any non-empty value; colorprofile
// itself only accepts values strconv.ParseBool understands.
func colorProfile() colorprofile.Profile {
	if os.Getenv("NO_COLOR") != "" {
		return colorprofile.ASCII
	}
	return colorprofile.Detect(os.Stdout, os.Environ())
}

// printVersion prints fstui's version, the compiled-in modules, and what
// each module found on this host.
func printVersion(w io.Writer, mods *registry.Registry) int {
	if code := report(w, 0, "fstui %s\nmodules: %s\n", buildVersion(), mods.Summary()); code != 0 {
		return code
	}
	for _, m := range mods.Modules() {
		if m.Describe == nil {
			continue
		}
		if code := report(w, 0, "%s: %s\n", m.Name, m.Describe(context.Background())); code != 0 {
			return code
		}
	}
	return 0
}

// report writes a message and returns code, or exitWrite if the message
// could not be written.
func report(w io.Writer, code int, format string, a ...any) int {
	if _, err := fmt.Fprintf(w, format, a...); err != nil {
		return exitWrite
	}
	return code
}

// dashboardView is the dashboard's view number (the 0 key).
const dashboardView = 10

// poolsView is view 1, where fstui opens unless ui.start_view says
// otherwise.
const poolsView = 1

// startView is the view fstui opens on: the dashboard with --dashboard,
// else ui.start_view, by its place in config.Views (view 0 is number 10).
func startView(dashboard bool, name string) int {
	i := slices.Index(config.Views, name)
	switch {
	case dashboard || i == 0:
		return dashboardView
	case i < 0:
		return poolsView
	}
	return i
}

// newSchedules is the Config view's Schedules tab: the backends' tasks
// and fstui's own jobs, read from this system (the demo gives its own
// rows), with the write module's changes added to the backends'.
func newSchedules(files []string, cfg config.Config, backends *opened) *ui.Schedules {
	sched := schedule.Reader{
		Host: schedule.Detect("/"), Tasks: backends.scheduleTasks, Run: backends.scheduleRun,
		Settings: func() config.Schedules { c, _ := config.Load(files...); return c.Schedules }, // as saved now
	}
	if backends.scheduleHost != nil {
		sched.Host = *backends.scheduleHost
	} else if t, err := execute.ResolveOptional(nil, []execute.Tool{execute.Systemctl}); err == nil {
		sched.Run = execute.NewRunner(t)
	}
	var cannot string
	backends.write, cannot = withSchedules(backends.write, sched)
	return ui.NewSchedules(sched.Jobs, cannot, cfg.Limits.ReadTimeout, time.Now)
}

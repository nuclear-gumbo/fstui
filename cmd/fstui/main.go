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
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/config"
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
	return r
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usageHeader precedes the option list of --help.
const usageHeader = `Usage: fstui [options]

A terminal UI for looking at ZFS pools, datasets, snapshots and volumes.
`

// opened is what the compiled-in backends opened: their views, and what
// they give the write module.
type opened struct {
	screens []ui.Screen
	write   writeHooks
}

func run(args []string, stdout, stderr io.Writer) int {
	// GNU-style options: --long, and -x for the few that have a short form.
	fs := pflag.NewFlagSet("fstui", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.SortFlags = false
	showVersion := fs.BoolP("version", "V", false, "print the version, compiled-in modules and OpenZFS versions, then exit")
	listThemes := fs.Bool("list-themes", false, "list the themes fstui can use, then exit")
	superuser := fs.Bool("superuser", false, "hide advisory warnings, never confirmations (also superuser: true in config.yaml)")
	dashboard := fs.Bool("dashboard", false, "start on the dashboard (view 0), for a screen left on all day")
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
	return start(stderr, *superuser, *dashboard, wf)
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
func start(stderr io.Writer, superuserFlag, dashboard bool, wf *writeFlags) int {
	files := []string{config.SystemFile}
	var notices []string
	if f, err := config.UserFile(); err != nil {
		notices = append(notices, err.Error())
	} else {
		files = append(files, f)
	}
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

	backends, err := zfsScreens(cfg)
	if err != nil {
		return report(stderr, exitFailed, "fstui: %v\n", err)
	}
	actions, writeScreens, mode, closeWrite, writeNotices := startWrite(wf, backends.write)
	defer closeWrite()
	notices = append(notices, writeNotices...)
	screens := append(append(backends.screens, writeScreens...), ui.NewSettings(files, cfg, config.ThemeNames(config.ThemeDirs()...), config.Load,
		func(name string) (theme.Theme, []config.Problem) {
			return config.LoadTheme(name, config.ThemeDirs()...)
		}))

	host, _ := os.Hostname() // empty is shown as empty; nothing depends on it
	session := ui.Session{Host: termtext.New(host), Privilege: privilege(), Label: buildLabel, Mode: mode, Superuser: cfg.Superuser, Notices: notices}
	profile := colorProfile()
	m := ui.New(session, screens, ui.Options{
		Theme: th, Profile: profile, Overrides: cfg.Colors, Exact: cfg.Units == config.Exact,
		ASCII: cfg.UseASCII(os.Getenv("TERM")), Mouse: cfg.Mouse, Natural: cfg.NaturalScrolling, Now: time.Now, StartView: startView(dashboard),
		Actions: actions,
	})
	if _, err := tea.NewProgram(m, tea.WithColorProfile(profile)).Run(); err != nil {
		return report(stderr, exitFailed, "fstui: %v\n", err)
	}
	return 0
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

// poolsView is view 1. fstui opens on it although the dashboard comes
// first in the view bar; --dashboard opens on the dashboard.
const poolsView = 1

// startView is the view fstui opens on.
func startView(dashboard bool) int {
	if dashboard {
		return dashboardView
	}
	return poolsView
}

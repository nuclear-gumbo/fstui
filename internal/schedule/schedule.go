// SPDX-License-Identifier: GPL-3.0-or-later

// Package schedule reads, and with the write module plans, when fstui's
// periodic work runs: the scrubs and trims a storage backend asks for,
// fstui check and fstui watch, under systemd, cron or OpenRC. It names no
// backend: each lists its Tasks.
package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Files fstui writes, and where it reads what is enabled.
const (
	UnitDir        = "/etc/systemd/system"
	timersWants    = UnitDir + "/timers.target.wants"
	multiUserWants = UnitDir + "/multi-user.target.wants"
	CronFile       = "/etc/cron.d/fstui"
	InitScript     = "/etc/init.d/fstui-watch"
	runlevelLink   = "/etc/runlevels/default/fstui-watch"

	CheckService = "fstui-check.service"
	CheckTimer   = "fstui-check.timer"
	WatchService = "fstui-watch.service"
	WatchInit    = "fstui-watch" // the OpenRC service
)

// Job IDs of fstui's own work; a backend's are Kind:Target.
const (
	CheckID = "check"
	WatchID = "watch"
)

// Values a job can be set to.
const (
	Off     = "off"
	On      = "on"
	Weekly  = "weekly"
	Monthly = "monthly"
)

// unitDirs hold systemd's unit templates, the administrator's first.
var unitDirs = []string{UnitDir, "/usr/lib/systemd/system", "/lib/systemd/system"}

// cronDaemons are the cron daemons that read /etc/cron.d: Debian's cron,
// cronie and dcron.
var cronDaemons = []string{"/usr/sbin/cron", "/usr/sbin/crond", "/usr/bin/crond", "/sbin/crond"}

// Host is what runs scheduled work here.
type Host struct {
	Root    string // "/"; a directory standing in for it in tests
	Systemd bool   // systemd is the running init (/run/systemd/system)
	OpenRC  bool   // OpenRC is (/run/openrc)
	Cron    bool   // a cron daemon that reads /etc/cron.d is installed
}

// Detect finds what runs scheduled work on the system at root.
func Detect(root string) Host {
	h := Host{Root: root}
	h.Systemd = h.isDir("/run/systemd/system")
	h.OpenRC = h.isDir("/run/openrc")
	h.Cron = h.isDir("/etc/cron.d") && slices.ContainsFunc(cronDaemons, h.exists)
	return h
}

func (h Host) path(p string) string { return filepath.Join(h.Root, p) }

func (h Host) exists(p string) bool {
	_, err := os.Lstat(h.path(p))
	return err == nil
}

func (h Host) isDir(p string) bool {
	st, err := os.Stat(h.path(p))
	return err == nil && st.IsDir()
}

// read returns a file's contents; a missing file is empty and not an
// error.
func (h Host) read(p string) ([]byte, bool, error) {
	b, err := os.ReadFile(h.path(p))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", p, err)
	}
	return b, true, nil
}

// hasTemplate reports whether a systemd unit template is installed.
func (h Host) hasTemplate(unit string) bool {
	return slices.ContainsFunc(unitDirs, func(d string) bool { return h.exists(filepath.Join(d, unit)) })
}

// wanted lists the units enabled into a target's .wants directory.
func (h Host) wanted(dir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(h.path(dir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

// Task is periodic work a storage backend asks for, such as scrubbing a
// pool.
type Task struct {
	Kind   string // "scrub" or "trim"
	Target string // what it is done to: a pool
	// Templates are the backend's systemd timer templates by period
	// (Weekly: "zfs-scrub-weekly@.timer"); Target is their instance.
	Templates map[string]string
	// Command is what cron runs, with absolute paths.
	Command []string
}

// ID identifies the task's job.
func (t Task) ID() string { return t.Kind + ":" + t.Target }

// Job is one row of the Schedules tab.
type Job struct {
	ID   string // CheckID, WatchID or a Task's ID; empty for one fstui does not manage
	What string // "scrub tank"
	When string // Off, Weekly, Monthly, "every 15m", On
	By   string // what runs it: "systemd timer zfs-scrub-weekly@tank.timer"
	// Next and Last are when it runs next and ran last, when known.
	Next, Last time.Time
	// Choices are what fstui can set it to here; nil when it cannot.
	Choices []string
	// Note says why it cannot be changed, or what to know about it.
	Note string
}

// Runner reads systemctl's output: execute.Runner.
type Runner interface {
	Read(ctx context.Context, a execute.Argv) (execute.Result, error)
}

// Reader reads the schedules.
type Reader struct {
	Host Host
	Run  Runner // nil without systemctl
	// Tasks lists the backends' tasks, and schedules of their work that
	// fstui did not write and does not change (Debian's scrub cron job).
	Tasks    func(ctx context.Context) ([]Task, []Job, error)
	Settings func() config.Schedules
}

// timer is a timer's times from systemctl list-timers.
type timer struct{ next, last time.Time }

// Jobs reads every schedule: the backends' tasks, fstui check and fstui
// watch, then the schedules fstui does not manage.
func (r Reader) Jobs(ctx context.Context) ([]Job, error) {
	s, err := r.state(ctx)
	if err != nil {
		return nil, err
	}
	tasks, others, err := r.tasks(ctx)
	if err != nil {
		return nil, err
	}
	var out []Job
	for _, t := range tasks {
		out = append(out, r.taskJob(s, t))
	}
	out = append(out, r.checkJob(s), r.watchJob(s))
	return append(out, others...), nil
}

func (r Reader) tasks(ctx context.Context) ([]Task, []Job, error) {
	if r.Tasks == nil {
		return nil, nil, nil
	}
	return r.Tasks(ctx)
}

// state is what is enabled now.
type state struct {
	timers  map[string]timer // by unit, from list-timers
	enabled map[string]bool  // units in the .wants directories
	cron    map[string]cronEntry
	units   map[string]string // fstui's unit files, by name, as written now
	openrc  bool              // fstui-watch is in the default runlevel
}

func (r Reader) state(ctx context.Context) (state, error) {
	s := state{timers: map[string]timer{}, enabled: map[string]bool{}, units: map[string]string{}}
	h := r.Host
	if h.Systemd {
		for k := range h.wanted(timersWants) {
			s.enabled[k] = true
		}
		for k := range h.wanted(multiUserWants) {
			s.enabled[k] = true
		}
		for _, u := range []string{CheckService, CheckTimer, WatchService} {
			b, ok, err := h.read(filepath.Join(UnitDir, u))
			if err != nil {
				return s, err
			}
			if ok {
				s.units[u] = string(b)
			}
		}
		if r.Run != nil {
			t, err := r.listTimers(ctx)
			if err != nil {
				return s, err
			}
			s.timers = t
		}
	}
	b, _, err := h.read(CronFile)
	if err != nil {
		return s, err
	}
	s.cron = parseCron(b)
	s.openrc = h.OpenRC && h.exists(runlevelLink)
	return s, nil
}

func (r Reader) listTimers(ctx context.Context) (map[string]timer, error) {
	res, err := r.Run.Read(ctx, execute.Argv{Tool: execute.Systemctl, Options: []string{"list-timers", "--all", "--output=json", "--no-pager"}})
	if errors.Is(err, execute.ErrNotResolved) {
		return map[string]timer{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading timers: %w", err)
	}
	var list []struct {
		Next uint64 `json:"next"`
		Last uint64 `json:"last"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal(res.Stdout, &list); err != nil {
		return nil, fmt.Errorf("systemctl list-timers: %w", err)
	}
	out := map[string]timer{}
	for _, t := range list {
		out[t.Unit] = timer{next: micros(t.Next), last: micros(t.Last)}
	}
	return out, nil
}

// micros is systemd's time in microseconds since 1970; 0 is unknown.
func micros(us uint64) time.Time {
	if us == 0 {
		return time.Time{}
	}
	return time.UnixMicro(int64(us)) //nolint:gosec // microseconds since 1970
}

// manager is what fstui uses for timed work here: systemd if it runs,
// else cron.
func (h Host) manager() string {
	switch {
	case h.Systemd:
		return "systemd"
	case h.Cron:
		return "cron"
	}
	return ""
}

// noScheduler says why nothing can be scheduled here.
const noScheduler = "neither systemd nor a cron daemon reading /etc/cron.d was found"

func (r Reader) taskJob(s state, t Task) Job {
	j := Job{ID: t.ID(), What: t.Kind + " " + t.Target, When: Off}
	switch r.Host.manager() {
	case "systemd":
		j.Choices = []string{Off}
		for _, period := range []string{Weekly, Monthly} {
			tmpl := t.Templates[period]
			if tmpl == "" || !r.Host.hasTemplate(tmpl) {
				continue
			}
			j.Choices = append(j.Choices, period)
			unit := Instance(tmpl, t.Target)
			if s.enabled[unit] && j.When == Off {
				j.When, j.By = period, "systemd timer "+unit
				j.Next, j.Last = s.timers[unit].next, s.timers[unit].last
			}
		}
		if len(j.Choices) == 1 {
			names := slices.Sorted(maps.Values(t.Templates))
			j.Choices, j.Note = nil, strings.Join(names, " and ")+" are not installed"
		}
	case "cron":
		j.Choices = []string{Off, Weekly, Monthly}
		if e, ok := s.cron[j.ID]; ok {
			j.When, j.By = e.when, CronFile
		}
	default:
		j.Note = noScheduler
	}
	return j
}

func (r Reader) checkJob(s state) Job {
	j := Job{ID: CheckID, What: "fstui check", When: Off}
	switch r.Host.manager() {
	case "systemd":
		j.Choices = []string{Off, On}
		if s.enabled[CheckTimer] {
			j.When, j.By = "every "+timerEvery(s.units[CheckTimer]), "systemd timer "+CheckTimer
			j.Next, j.Last = s.timers[CheckTimer].next, s.timers[CheckTimer].last
		}
	case "cron":
		j.Choices = []string{Off, On}
		if e, ok := s.cron[CheckID]; ok {
			j.When, j.By = e.when, CronFile
		}
	default:
		j.Note = noScheduler
	}
	if every := r.settings().CheckEvery; j.When != Off && j.When != "every "+every {
		j.Note = "schedules.check_every says every " + every + "; turning it on again applies that"
	}
	return j
}

func (r Reader) watchJob(s state) Job {
	j := Job{ID: WatchID, What: "fstui watch", When: Off}
	switch {
	case r.Host.Systemd:
		j.Choices = []string{Off, On}
		if s.enabled[WatchService] {
			j.When, j.By = On, "systemd service "+WatchService
		}
	case r.Host.OpenRC:
		j.Choices = []string{Off, On}
		if s.openrc {
			j.When, j.By = On, "OpenRC service "+WatchInit
		}
	default:
		j.Note = "fstui watch runs as a service under systemd or OpenRC; neither was found"
	}
	return j
}

// onActive reads OnUnitActiveSec from a timer unit fstui wrote.
var onActive = regexp.MustCompile(`(?m)^OnUnitActiveSec=(\S+)$`)

// timerEvery is the interval of fstui's check timer, as written.
func timerEvery(unit string) string {
	if m := onActive.FindStringSubmatch(unit); m != nil {
		return m[1]
	}
	return "?"
}

// Instance is the unit of a systemd template for an instance, escaped
// as systemd-escape does (systemd.unit(5)): "-" and other characters a
// unit name cannot hold become \xNN, so %i gives the name back.
func Instance(template, instance string) string {
	var b strings.Builder
	for i := 0; i < len(instance); i++ {
		c := instance[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == ':' || c == '.' && i > 0
		if ok {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	prefix, suffix, _ := strings.Cut(template, "@")
	return prefix + "@" + b.String() + suffix
}

// settings are the schedules settings now, or the defaults.
func (r Reader) settings() config.Schedules {
	if r.Settings == nil {
		return config.Default().Schedules
	}
	return r.Settings()
}

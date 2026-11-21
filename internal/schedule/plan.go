// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package schedule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Builder plans schedule changes.
type Builder struct {
	Reader Reader
	// Binary is the fstui that systemd, cron or OpenRC will run, checked
	// with execute.Trusted: they run it as root.
	Binary func() (string, error)
	// Has reports whether a tool was resolved.
	Has          func(execute.Tool) bool
	Unprivileged bool
}

// Build builds a plan from saved parameters.
func (b Builder) Build(ctx context.Context, _ mutate.Op, params map[string]string) (*mutate.Plan, error) {
	return b.Set(ctx, params["job"], params["when"])
}

// Set plans setting job id to when (Off, On, Weekly, Monthly).
func (b Builder) Set(ctx context.Context, id, when string) (*mutate.Plan, error) {
	s, err := b.Reader.state(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := b.Reader.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(jobs, func(j Job) bool { return j.ID == id && id != "" })
	if i < 0 {
		return nil, fmt.Errorf("no schedule %q", id)
	}
	job := jobs[i]
	if !slices.Contains(job.Choices, when) {
		return nil, fmt.Errorf("%s cannot be set to %s here", job.What, when)
	}
	summary := "Schedule " + job.What + ": " + when
	if when == Off {
		summary = "Stop the schedule of " + job.What
	}
	p := &mutate.Plan{ID: mutate.NewID(), Op: mutate.ScheduleSet, Summary: summary, Targets: []string{job.What}, Params: map[string]string{"job": id, "when": when}}
	block(p, b.Unprivileged, "changing schedules needs root")
	ch := change{b: b, s: s, p: p, job: job, when: when}
	switch id {
	case CheckID:
		ch.check()
	case WatchID:
		err = ch.watch()
	default:
		err = ch.task(ctx)
	}
	if err != nil {
		return nil, err
	}
	block(p, len(p.Steps) == 0 && len(p.Blocked) == 0, job.What+" is already "+when)
	files := ch.watched()
	p.Baseline = b.fingerprint(s, files)
	p.Recheck = func(ctx context.Context) (mutate.Fingerprint, error) {
		s, err := b.Reader.state(ctx)
		return b.fingerprint(s, files), err
	}
	return p, nil
}

// change is one plan being built.
type change struct {
	b       Builder
	s       state
	p       *mutate.Plan
	job     Job
	when    string
	written []string // files the plan writes
	shown   strings.Builder
}

func block(p *mutate.Plan, cond bool, reason string) {
	if cond {
		p.Blocked = append(p.Blocked, reason)
	}
}

func affects(p *mutate.Plan, s string) {
	p.Preview.Affects = append(p.Preview.Affects, termtext.New(s))
}

// need blocks the plan unless every tool was resolved.
func (c *change) need(tools ...execute.Tool) {
	for _, t := range tools {
		block(c.p, c.b.Has == nil || !c.b.Has(t), string(t)+" was not found in the system directories")
	}
}

// binary is the trusted fstui to name in a unit, a cron line or a
// script; the plan is blocked when there is none.
func (c *change) binary() string {
	if c.b.Binary == nil {
		block(c.p, true, "fstui cannot find its own file, which the schedule must name; start it by its full path")
		return ""
	}
	bin, err := c.b.Binary()
	if err != nil {
		block(c.p, true, "fstui would run as root from a file others could replace ("+err.Error()+"); install it under /usr")
		return ""
	}
	return bin
}

// write adds the steps that replace path with content, mode, through a
// hidden file beside it and a rename, so nothing reads half a file; the
// confirmation shows the content in full.
func (c *change) write(path, content, mode string) {
	c.need(execute.Tee, execute.Chmod, execute.Mv)
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".fstui-new")
	c.p.Steps = append(c.p.Steps,
		execute.Argv{Tool: execute.Tee, Operands: []string{tmp}, Input: []byte(content)},
		execute.Argv{Tool: execute.Chmod, Options: []string{mode}, Operands: []string{tmp}},
		execute.Argv{Tool: execute.Mv, Operands: []string{tmp, path}})
	c.written = append(c.written, path)
	fmt.Fprintf(&c.shown, "── %s (mode %s)\n%s", path, mode, content)
	c.p.Preview.Output, c.p.Preview.OutputTitle = termtext.New(c.shown.String()), "Files written, in full"
}

func (c *change) systemctl(args ...string) {
	c.need(execute.Systemctl)
	c.p.Steps = append(c.p.Steps, execute.Argv{Tool: execute.Systemctl, Options: args[:len(args)-1], Operands: args[len(args)-1:]})
}

func (c *change) daemonReload() {
	c.need(execute.Systemctl)
	c.p.Steps = append(c.p.Steps, execute.Argv{Tool: execute.Systemctl, Operands: []string{"daemon-reload"}, NoSeparator: true})
}

// task turns a backend's task to when, under systemd or cron.
func (c *change) task(ctx context.Context) error {
	tasks, _, err := c.b.Reader.tasks(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(tasks, func(t Task) bool { return t.ID() == c.job.ID })
	if i < 0 {
		return fmt.Errorf("no task %q", c.job.ID)
	}
	t := tasks[i]
	if c.b.Reader.Host.manager() == "cron" {
		c.cron(t.ID(), c.when, func() string { return taskCron(t, c.when, c.b.Reader.settings()) })
		return nil
	}
	for _, period := range []string{Weekly, Monthly} {
		unit := Instance(t.Templates[period], t.Target)
		switch {
		case c.s.enabled[unit] && period != c.when:
			c.systemctl("disable", "--now", unit)
			affects(c.p, "stops "+unit)
		case !c.s.enabled[unit] && period == c.when:
			c.systemctl("enable", "--now", unit)
			affects(c.p, "starts "+unit+", a "+period+" timer installed already: no unit file is written")
			// OpenZFS's timers are Persistent: one that has not run in its
			// period (or never) runs at once (seen on systemd 257).
			affects(c.p, "the first "+t.Kind+" of "+t.Target+" may start right away: the timer catches up a run it missed or never made")
		}
	}
	return nil
}

// cron rewrites fstui's cron file with entry id set to when (removed
// for Off), its line made by line.
func (c *change) cron(id, when string, line func() string) {
	entries := maps.Clone(c.s.cron)
	if when == Off {
		delete(entries, id)
	} else {
		entries[id] = cronEntry{when: when, line: line()}
	}
	if old, ok := c.s.cron[id]; ok && when != Off && old == entries[id] {
		return // already so
	}
	if _, ok := c.s.cron[id]; !ok && when == Off {
		return
	}
	c.write(CronFile, cronFile(entries), "0644")
	affects(c.p, "cron reads "+CronFile+" again within a minute; nothing is restarted")
}

// check turns fstui check's schedule on (at schedules.check_every) or
// off.
func (c *change) check() {
	every := c.b.Reader.settings().CheckEvery
	block(c.p, c.when == On && c.jobOn(WatchID), "fstui watch runs already and sends the same notifications; turn it off first")
	if c.b.Reader.Host.manager() == "cron" {
		if c.when == On {
			bin := c.binary()
			c.cron(CheckID, "every "+every, func() string { return checkCron(bin, every) })
		} else {
			c.cron(CheckID, Off, nil)
		}
		return
	}
	if c.when == Off {
		if c.s.enabled[CheckTimer] {
			c.systemctl("disable", "--now", CheckTimer)
			affects(c.p, "stops "+CheckTimer+"; its files stay in "+UnitDir)
		}
		return
	}
	bin := c.binary()
	service, timer := checkUnits(bin, every)
	svcChanged := c.unit(CheckService, service) // both written: || would skip the timer
	changed := c.unit(CheckTimer, timer) || svcChanged
	if changed {
		c.daemonReload()
	}
	switch {
	case !c.s.enabled[CheckTimer]:
		c.systemctl("enable", "--now", CheckTimer)
		affects(c.p, "fstui check runs every "+every+", first 5 minutes after boot")
	case changed:
		c.systemctl("restart", CheckTimer)
		affects(c.p, "fstui check now runs every "+every)
	}
}

// unit writes a unit file unless it already says content.
func (c *change) unit(name, content string) bool {
	if c.s.units[name] == content {
		return false
	}
	c.write(filepath.Join(UnitDir, name), content, "0644")
	return true
}

// watch turns the fstui watch service on or off, under systemd or
// OpenRC.
func (c *change) watch() error {
	block(c.p, c.when == On && c.jobOn(CheckID), "fstui check runs on a schedule already and sends the same notifications; turn it off first")
	if c.b.Reader.Host.Systemd {
		if c.when == Off {
			if c.s.enabled[WatchService] {
				c.systemctl("disable", "--now", WatchService)
				affects(c.p, "stops fstui watch; "+WatchService+" stays in "+UnitDir)
			}
			return nil
		}
		changed := c.unit(WatchService, watchUnit(c.binary()))
		if changed {
			c.daemonReload()
		}
		switch {
		case !c.s.enabled[WatchService]:
			c.systemctl("enable", "--now", WatchService)
			affects(c.p, "fstui watch starts now and at every boot; journalctl -u "+WatchService+" shows what it sent")
		case changed:
			c.systemctl("restart", WatchService)
		}
		return nil
	}
	c.need(execute.RcUpdate, execute.RcService)
	if c.when == Off {
		if c.s.openrc {
			c.p.Steps = append(c.p.Steps,
				execute.Argv{Tool: execute.RcService, Operands: []string{WatchInit, "stop"}, NoSeparator: true},
				execute.Argv{Tool: execute.RcUpdate, Operands: []string{"del", WatchInit, "default"}, NoSeparator: true})
			affects(c.p, "stops fstui watch; "+InitScript+" stays")
		}
		return nil
	}
	script := openRCScript(c.binary())
	old, _, err := c.b.Reader.Host.read(InitScript)
	if err != nil {
		return err
	}
	if string(old) != script {
		c.write(InitScript, script, "0755")
	}
	if !c.s.openrc {
		c.p.Steps = append(c.p.Steps,
			execute.Argv{Tool: execute.RcUpdate, Operands: []string{"add", WatchInit, "default"}, NoSeparator: true},
			execute.Argv{Tool: execute.RcService, Operands: []string{WatchInit, "start"}, NoSeparator: true})
		affects(c.p, "fstui watch starts now and at every boot; it logs to /var/log/fstui-watch.log")
	} else if string(old) != script {
		c.p.Steps = append(c.p.Steps, execute.Argv{Tool: execute.RcService, Operands: []string{WatchInit, "restart"}, NoSeparator: true})
	}
	return nil
}

// jobOn reports whether fstui's job id is on now.
func (c *change) jobOn(id string) bool {
	switch id {
	case CheckID:
		_, cron := c.s.cron[CheckID]
		return c.s.enabled[CheckTimer] || cron
	case WatchID:
		return c.s.enabled[WatchService] || c.s.openrc
	}
	return false
}

// watched are the files a plan's re-check reads: those it writes and
// fstui's others.
func (c *change) watched() []string {
	return slices.Compact(slices.Sorted(slices.Values(append([]string{
		CronFile, InitScript,
		filepath.Join(UnitDir, CheckService), filepath.Join(UnitDir, CheckTimer), filepath.Join(UnitDir, WatchService),
	}, c.written...))))
}

// fingerprint is what the plan depends on: the files, and which units
// are enabled.
func (b Builder) fingerprint(s state, files []string) mutate.Fingerprint {
	f := mutate.Fingerprint{}
	for _, p := range files {
		data, ok, err := b.Reader.Host.read(p)
		switch {
		case err != nil:
			f[p] = "unreadable"
		case !ok:
			f[p] = mutate.Absent
		default:
			h := sha256.Sum256(data)
			f[p] = hex.EncodeToString(h[:8])
		}
	}
	var on []string
	for u := range s.enabled {
		on = append(on, u)
	}
	slices.Sort(on)
	f["enabled"] = strings.Join(on, " ")
	f["openrc"] = fmt.Sprint(s.openrc)
	return f
}

// SPDX-License-Identifier: GPL-3.0-or-later

// Package health finds the problems in a set of ZFS pools: what the Pools
// view lists under its table, what the dashboard counts, and what fstui
// check notifies about. One place decides what a ZFS problem is, so they
// agree; the problems themselves are backend-neutral (internal/problem).
package health

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/events"

	"github.com/nuclear-gumbo/fstui/internal/problem"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/format"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// Options are what Check compares against.
type Options struct {
	Thresholds config.Thresholds
	Version    zfs.Version // a userland and kernel module mismatch is a problem
	Now        time.Time
	// ScrubNote, if set, completes an overdue-scrub problem with whether
	// a scrub is coming ("; the next is in 3d").
	ScrubNote func(pool string) string
}

// Check lists the problems of the given pools.
func Check(pools []zfs.Overview, o Options) []problem.Problem {
	var ps []problem.Problem
	if v := o.Version; v.Mismatched() {
		text := fmt.Sprintf("Userland %s, kernel module %s. Reboot or reload the module so they match.",
			termtext.New(v.Userland).Display(), termtext.New(v.Kernel).Display())
		ps = append(ps, problem.Problem{Key: "version", State: v.Userland + " " + v.Kernel, Severity: problem.Warning, Text: text, Advisory: true})
	}
	for _, p := range pools {
		ps = append(ps, poolProblems(p, o)...)
	}
	return ps
}

func poolProblems(o zfs.Overview, opt Options) []problem.Problem {
	name := o.Pool.Name.String()
	th := opt.Thresholds
	var ps []problem.Problem
	if o.Status.State != "ONLINE" {
		ps = append(ps, problem.Problem{Key: "health:" + name, State: o.Status.State, Severity: problem.Error, Text: fmt.Sprintf("%s is %s%s", name, o.Status.State, deviceProblems(o.Status))})
	}
	if n := o.Status.ErrorCount; n > 0 {
		ps = append(ps, problem.Problem{Key: "data:" + name, Severity: problem.Error, Text: fmt.Sprintf("%s has %d data errors; zpool status -v %s lists the affected files", name, n, name)})
	}
	if pct := CapacityPct(o); pct >= th.CapacityWarn {
		p := problem.Problem{Key: "capacity:" + name, State: "WARN", Severity: problem.Warning, Text: fmt.Sprintf("%s is %d%% full (warning from %d%%)", name, pct, th.CapacityWarn), Advisory: true}
		if pct >= th.CapacityError {
			p.State, p.Severity = "FULL", problem.Error
		}
		ps = append(ps, p)
	}
	if frag, ok := o.Pool.Properties.Number("fragmentation"); ok && int(frag) >= th.FragmentationWarn { //nolint:gosec // a percentage
		ps = append(ps, problem.Problem{Key: "fragmentation:" + name, State: "over", Severity: problem.Warning, Text: fmt.Sprintf("free space in %s is %d%% fragmented; writes may slow down", name, frag), Advisory: true})
	}
	if sc := o.Status.Scan; sc != nil && sc.Function == "SCRUB" && !sc.End.IsZero() {
		if age := opt.Now.Sub(sc.End); age > time.Duration(th.ScrubOverdueDays)*24*time.Hour {
			text := fmt.Sprintf("%s was last scrubbed %s ago (overdue after %d days)", name, format.Age(age), th.ScrubOverdueDays)
			if opt.ScrubNote != nil {
				text += opt.ScrubNote(name)
			}
			ps = append(ps, problem.Problem{Key: "scrub:" + name, State: sc.End.String(), Severity: problem.Warning, Text: text, Advisory: true})
		}
	}
	if c := o.Status.Checkpoint; c != nil && opt.Now.Sub(c.Taken) > th.CheckpointAge {
		ps = append(ps, problem.Problem{
			Key: "checkpoint:" + name, State: c.Taken.String(), Severity: problem.Warning, Advisory: true,
			Text: fmt.Sprintf("%s has held a checkpoint for %s (%s); remove, attach, detach, split and reguid are blocked until it is discarded",
				name, format.Age(opt.Now.Sub(c.Taken)), format.Size(c.Space, false)),
		})
	}
	return ps
}

// CapacityPct is how full the pool is for writers: the root dataset's
// used share of used plus available.
func CapacityPct(o zfs.Overview) int {
	total := o.Used + o.Available
	if total == 0 {
		return 0
	}
	return int((o.Used*100 + total/2) / total) //nolint:gosec // at most 100
}

// deviceProblems summarises the devices that are not ONLINE, e.g.
// ": 1 device OFFLINE".
func deviceProblems(st zfs.PoolStatus) string {
	counts := map[string]int{}
	st.Walk(func(v zfs.Vdev) {
		if len(v.Children) == 0 && v.State != "ONLINE" && v.State != "AVAIL" && v.State != "INUSE" {
			counts[v.State]++
		}
	})
	if len(counts) == 0 {
		return ""
	}
	states := make([]string, 0, len(counts))
	for s := range counts {
		states = append(states, s)
	}
	slices.Sort(states)
	parts := make([]string, len(states))
	for i, s := range states {
		parts[i] = fmt.Sprintf("%d %s %s", counts[s], format.Plural(counts[s], "device", "devices"), s)
	}
	return ": " + strings.Join(parts, ", ")
}

// Source is the ZFS backend as fstui check reads it: the pools' measures
// and the kernel's events.
type Source struct {
	Reader *zfs.Reader
	// Options are what Check compares against; Now is set on each read.
	Options Options
}

// Events are the kernel's events (zpool events).
func (s Source) Events(ctx context.Context) ([]events.Event, int, error) {
	return s.Reader.Events(ctx) //nolint:wrapcheck // as above
}

// Measures reads every pool and reports the measures alerts compare
// (config.Metrics): capacity, free space, health, devices and their
// errors, data errors, scrubs, resilvers and checkpoints from zpool; read
// and write wait, when need asks for them, from one second of zpool
// iostat. The system's own subject reports whether the userland and
// kernel module versions differ.
func (s Source) Measures(ctx context.Context, now time.Time, need map[string]bool) ([]problem.Measure, error) {
	pools, err := s.Reader.Overviews(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the reader names the command
	}
	out := Measure(pools, now, s.Options.Version)
	if need["read_wait"] || need["write_wait"] {
		addWaits(ctx, s.Reader, out)
	}
	return out, nil
}

// Measure is what zpool list and status say about the pools, and the
// system's own subject with the version check.
func Measure(pools []zfs.Overview, now time.Time, v zfs.Version) []problem.Measure {
	differ, detail := 0.0, ""
	if v.Mismatched() {
		differ = 1
		detail = fmt.Sprintf(" (userland %s, kernel module %s)", termtext.New(v.Userland).Display(), termtext.New(v.Kernel).Display())
	}
	out := []problem.Measure{{Numbers: map[string]float64{"versions_differ": differ}, Details: map[string]string{"versions_differ": detail}}}
	for _, o := range pools {
		out = append(out, poolMeasure(o, now))
	}
	return out
}

// poolMeasure is what zpool list and status say about one pool.
func poolMeasure(o zfs.Overview, now time.Time) problem.Measure {
	st := o.Status
	m := problem.Measure{
		Subject: o.Pool.Name.String(),
		Numbers: map[string]float64{
			"capacity": float64(CapacityPct(o)), "free": float64(o.Available), "data_errors": float64(st.ErrorCount),
		},
		States:  map[string]string{"health": st.State},
		Details: map[string]string{"health": deviceProblems(st), "devices_down": deviceProblems(st)},
	}
	if frag, ok := o.Pool.Properties.Number("fragmentation"); ok {
		m.Numbers["fragmentation"] = float64(frag)
	}
	down, errs := 0, uint64(0)
	st.Walk(func(v zfs.Vdev) {
		if len(v.Children) > 0 {
			return
		}
		if v.State != "ONLINE" && v.State != "AVAIL" && v.State != "INUSE" {
			down++
		}
		errs += v.ReadErrors + v.WriteErrors + v.ChecksumErrors
	})
	m.Numbers["devices_down"], m.Numbers["device_errors"] = float64(down), float64(errs)
	m.Numbers["resilvering"] = 0
	if sc := st.Scan; sc != nil {
		switch {
		case sc.Function == "SCRUB" && !sc.End.IsZero():
			m.Numbers["scrub_age"] = now.Sub(sc.End).Hours() / 24
		case sc.Function == "RESILVER" && sc.State == "SCANNING":
			m.Numbers["resilvering"] = 1
			if sc.ToExamine > 0 {
				m.Details["resilvering"] = fmt.Sprintf(", %d%% done", sc.Examined*100/sc.ToExamine)
			}
		}
	}
	if c := st.Checkpoint; c != nil {
		m.Numbers["checkpoint_age"] = now.Sub(c.Taken).Hours()
	}
	return m
}

// addWaits adds read_wait and write_wait, in milliseconds, from one
// interval of zpool iostat; a pool without I/O in it reports none.
func addWaits(ctx context.Context, r *zfs.Reader, ms []problem.Measure) {
	ctx, cancel := context.WithTimeout(ctx, ioSampleTimeout)
	defer cancel()
	var got zfs.IOSample
	_ = r.IOStats(ctx, func(s zfs.IOSample) { // a failed read leaves the waits unmeasured
		got = s
		cancel()
	})
	for _, p := range got {
		i := slices.IndexFunc(ms, func(m problem.Measure) bool { return m.Subject == p.Name.Raw() })
		if i < 0 {
			continue
		}
		if p.HasReadWait {
			ms[i].Numbers["read_wait"] = float64(p.ReadWait) / float64(time.Millisecond)
		}
		if p.HasWriteWait {
			ms[i].Numbers["write_wait"] = float64(p.WriteWait) / float64(time.Millisecond)
		}
	}
}

// ioSampleTimeout bounds the one interval of zpool iostat the waits come
// from: the first second is skipped (-y), so one sample takes about two.
const ioSampleTimeout = 10 * time.Second

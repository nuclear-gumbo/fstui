// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// ErrEventsNeedRoot is returned when zpool events refuses an unprivileged
// user, as it always does.
var ErrEventsNeedRoot = errors.New("zpool events needs root")

// Events reads every event the kernel still holds, oldest first, with
// zpool events -H -v. Polled, not followed: follow mode gives no usable
// output through a pipe on 2.3.9. dropped is how many events the kernel
// reported losing while they were read.
func (r *Reader) Events(ctx context.Context) (evs []events.Event, dropped int, err error) {
	res, err := r.run.Read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"events", "-H", "-v"}})
	var ce *execute.CommandError
	if errors.As(err, &ce) && strings.Contains(ce.Stderr.Raw(), "permission denied") {
		return nil, 0, ErrEventsNeedRoot
	}
	if err != nil {
		return nil, 0, fmt.Errorf("reading pool events: %w", err)
	}
	return ParseEvents(res.Stdout)
}

// ParseEvents parses zpool events -H -v: per event a line "<local
// time>\t<class>", then the payload as indented "name = value" lines,
// then a blank line. Nested payloads ("(embedded nvlist)" ... "(end
// name)") are skipped; the top-level fields carry what fstui shows.
func ParseEvents(data []byte) (evs []events.Event, dropped int, err error) {
	var p eventParser
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if err := p.line(sc.Text()); err != nil {
			return nil, 0, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, fmt.Errorf("zpool events: %w", err)
	}
	if err := p.finish(); err != nil {
		return nil, 0, err
	}
	return p.evs, p.dropped, nil
}

type eventParser struct {
	evs     []events.Event
	dropped int
	cur     *events.Event
	depth   int // inside nested payloads
}

func (p *eventParser) line(line string) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return p.finish()
	case strings.HasPrefix(line, "dropped ") && strings.HasSuffix(trimmed, " events"):
		n, err := strconv.Atoi(strings.Fields(trimmed)[1])
		if err != nil {
			return fmt.Errorf("zpool events: %q: %w", trimmed, err)
		}
		p.dropped += n
		return nil
	case !strings.HasPrefix(line, " "):
		return p.start(line)
	case p.cur == nil:
		return fmt.Errorf("zpool events: payload before an event: %q", termtext.New(line).Display())
	case strings.HasPrefix(trimmed, "(end "):
		p.depth--
		return nil
	}
	name, value, ok := strings.Cut(trimmed, " = ")
	if !ok {
		name, value = strings.TrimSuffix(trimmed, " ="), ""
	}
	if strings.HasPrefix(value, "(embedded nvlist)") || strings.HasPrefix(value, "(array of embedded nvlists)") {
		p.depth++
		return nil
	}
	if p.depth > 0 {
		return nil
	}
	return setField(p.cur, name, value)
}

// start begins an event at its "<time>\t<class>" line.
func (p *eventParser) start(line string) error {
	if err := p.finish(); err != nil {
		return err
	}
	_, class, ok := strings.Cut(line, "\t")
	if !ok {
		return fmt.Errorf("zpool events: unexpected line %q", termtext.New(line).Display())
	}
	p.cur, p.depth = &events.Event{Backend: "zfs", Class: termtext.New(class)}, 0
	return nil
}

func (p *eventParser) finish() error {
	e := p.cur
	if e == nil {
		return nil
	}
	if e.ID == 0 || e.Time.IsZero() {
		return fmt.Errorf("zpool events: event %s has no eid or time", e.Class.Display())
	}
	describe(e)
	p.evs, p.cur = append(p.evs, *e), nil
	return nil
}

// setField records one top-level payload field, and the ones fstui
// shows in columns.
func setField(e *events.Event, name, raw string) error {
	value := payloadValue(raw)
	e.Detail = append(e.Detail, events.Field{Name: name, Value: termtext.New(value)})
	switch name {
	case "eid":
		n, err := strconv.ParseUint(value, 0, 64)
		if err != nil {
			return fmt.Errorf("zpool events: eid %q: %w", value, err)
		}
		e.ID = n
	case "time":
		parts := strings.Fields(value)
		if len(parts) != 2 {
			return fmt.Errorf("zpool events: time %q", value)
		}
		sec, err1 := strconv.ParseInt(parts[0], 0, 64)
		nsec, err2 := strconv.ParseInt(parts[1], 0, 64)
		if err := errors.Join(err1, err2); err != nil {
			return fmt.Errorf("zpool events: time %q: %w", value, err)
		}
		e.Time = time.Unix(sec, nsec)
	case "pool":
		e.Source = termtext.New(value)
	case "vdev_path":
		e.Device = termtext.New(value)
	}
	return nil
}

// payloadValue unquotes a string value and drops the number zpool prints
// after an enumeration's name: `"OFFLINE" (0x2)` is OFFLINE.
func payloadValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, `"`) {
		if end := strings.LastIndex(raw, `"`); end > 0 {
			return raw[1:end]
		}
	}
	return raw
}

func field(e *events.Event, name string) string {
	for _, f := range e.Detail {
		if f.Name == name {
			return f.Value.Raw()
		}
	}
	return ""
}

// Class names below are those of OpenZFS 2.3.9 (include/sys/fm/fs/zfs.h,
// include/sys/sysevent/eventdefs.h, zpool-events(8)); a class missing
// here is shown by its own name.

// ereports are fault reports and their summaries; all are errors except
// a slow I/O.
var ereports = map[string]string{
	"checksum":           "Checksum error",
	"authentication":     "Authentication error on encrypted data",
	"io":                 "I/O error",
	"data":               "Data error",
	"delay":              "Slow I/O",
	"deadman":            "I/O hung (deadman)",
	"dio_verify_rd":      "Direct I/O read failed verification",
	"dio_verify_wr":      "Direct I/O write failed verification",
	"zpool":              "Pool could not be imported",
	"io_failure":         "I/O failure",
	"probe_failure":      "Device probe failed",
	"log_replay":         "Intent log could not be replayed",
	"config_cache_write": "Pool cache file could not be written",
}

// sysevents are routine notifications and their summaries.
var sysevents = map[string]string{
	"scrub_start":       "Scrub started",
	"scrub_finish":      "Scrub finished",
	"scrub_abort":       "Scrub stopped",
	"scrub_paused":      "Scrub paused",
	"scrub_resume":      "Scrub resumed",
	"errorscrub_start":  "Error scrub started",
	"errorscrub_finish": "Error scrub finished",
	"errorscrub_abort":  "Error scrub stopped",
	"errorscrub_paused": "Error scrub paused",
	"errorscrub_resume": "Error scrub resumed",
	"resilver_start":    "Resilver started",
	"resilver_finish":   "Resilver finished",
	"vdev_online":       "Device brought online",
	"vdev_add":          "Device added",
	"vdev_remove":       "Device removed from the pool",
	"vdev_attach":       "Device attached",
	"vdev_clear":        "Device errors cleared",
	"vdev_check":        "Device checked",
	"vdev_spare":        "Spare activated",
	"vdev_autoexpand":   "Device expanded",
	"config_sync":       "Configuration written",
	"pool_create":       "Pool created",
	"pool_destroy":      "Pool destroyed",
	"pool_import":       "Pool imported",
	"pool_export":       "Pool exported",
	"pool_reguid":       "Pool given a new GUID",
	"trim_start":        "Trim started",
	"trim_finish":       "Trim finished",
	"trim_cancel":       "Trim cancelled",
	"trim_suspend":      "Trim suspended",
	"trim_resume":       "Trim resumed",
}

// resources are changes to devices and datasets.
var resources = map[string]string{
	"removed":          "Device removed",
	"autoreplace":      "Device replaced automatically",
	"snapshot_mount":   "Snapshot mounted",
	"snapshot_unmount": "Snapshot unmounted",
	"zvol_create":      "Volume device created",
	"zvol_remove":      "Volume device removed",
}

const (
	ereportPrefix  = "ereport.fs.zfs."
	resourcePrefix = "resource.fs.zfs."
	syseventPrefix = "sysevent.fs.zfs."
)

// describe sets an event's severity and summary from its class.
func describe(e *events.Event) {
	class := e.Class.Raw()
	switch {
	case strings.HasPrefix(class, ereportPrefix):
		sub := strings.TrimPrefix(class, ereportPrefix)
		e.Severity, e.Summary = events.Error, termtext.New(ereports[sub])
		switch {
		case sub == "delay":
			e.Severity = events.Warn
		case strings.HasPrefix(sub, "vdev."):
			e.Summary = termtext.New("Device problem: " + strings.TrimPrefix(sub, "vdev."))
		}
		if e.Summary.IsEmpty() {
			e.Summary = termtext.New(sub)
		}
	case class == resourcePrefix+"statechange":
		state, was := field(e, "vdev_state"), field(e, "vdev_laststate")
		e.Severity = events.Info
		if state != "ONLINE" {
			e.Severity = events.Warn
		}
		e.Summary = termtext.New("Device " + state + " (was " + was + ")")
	case strings.HasPrefix(class, resourcePrefix):
		sub := strings.TrimPrefix(class, resourcePrefix)
		e.Severity, e.Summary = events.Info, termtext.New(resources[sub])
		if sub == "removed" {
			e.Severity = events.Warn
		}
		if e.Summary.IsEmpty() {
			e.Severity, e.Summary = events.Notice, termtext.New(sub)
		}
	case class == syseventPrefix+"history_event":
		e.Severity = events.Info
		// ZFS's own record, kept as it wrote it ("scan done errors=0"),
		// labelled so it is not read as fstui's wording.
		e.Summary = termtext.New("History: " + strings.TrimSpace(field(e, "history_internal_name")+" "+field(e, "history_internal_str")))
		if s := field(e, "history_str"); s != "" {
			e.Summary = termtext.New(s)
		}
	case strings.HasPrefix(class, syseventPrefix):
		sub := strings.TrimPrefix(class, syseventPrefix)
		e.Severity, e.Summary = events.Info, termtext.New(sysevents[sub])
		if e.Summary.IsEmpty() {
			e.Severity, e.Summary = events.Notice, termtext.New(sub)
		}
	default:
		e.Severity, e.Summary = events.Notice, e.Class
	}
}

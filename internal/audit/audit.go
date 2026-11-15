// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

// Package audit keeps the record of every change fstui runs: one JSON object
// per line in a root-only file, and the same record in syslog under
// LOG_AUTHPRIV.
//
// A record is written before each step and after it, so a crash in the
// middle of a step leaves an attempt without a result. When a write
// fails, the Log refuses every later record, which stops every later
// change.
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/syslog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Default locations.
const (
	DefaultDir  = "/var/log/fstui"
	DefaultFile = "audit.log"
)

// maxStderr is how much of a step's error output a record keeps.
const maxStderr = 4 << 10

// Record is one line of the audit log. The field names are part of the
// file format; add fields, never rename them.
type Record struct {
	Time       string   `json:"time"` // RFC 3339, UTC
	UID        int      `json:"uid"`
	SudoUser   string   `json:"sudo_user,omitempty"`
	Mode       string   `json:"mode"` // write or dry-run
	PlanID     string   `json:"plan_id"`
	Op         string   `json:"op"`
	PlanSHA256 string   `json:"plan_sha256,omitempty"`
	Step       int      `json:"step"`
	Phase      string   `json:"phase"` // attempt or result
	Argv       []string `json:"argv"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	DurationMS *int64   `json:"duration_ms,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
}

// Log appends records to the audit file and to syslog.
type Log struct {
	mu       sync.Mutex
	file     *os.File
	syslog   *syslog.Writer // nil when syslog is unavailable
	broken   error          // set by the first failed write
	uid      int
	sudoUser string
	now      func() time.Time
}

// ErrUnsafe is returned when the log's directory or file could be
// changed by someone other than the user fstui runs as.
var ErrUnsafe = errors.New("can be written by someone other than its owner")

// Open opens (creating if needed) dir/audit.log for appending. dir is
// created 0750 and must belong to the effective user and be writable by
// no one else; the file is created 0600 and must be a regular file with
// the same owner. In write mode, fstui refuses to start changes if Open
// fails.
func Open(dir string) (*Log, error) {
	uid := os.Geteuid()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	if err := checkOwned(dir, uid, true); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, DefaultFile)
	// The directory was checked above; O_NOFOLLOW refuses a planted link.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // see above
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	if err := checkOwned(path, uid, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	l := &Log{file: f, uid: uid, sudoUser: os.Getenv("SUDO_USER"), now: time.Now}
	// Syslog is a second copy; without a syslog daemon (containers) the
	// file is the record.
	if w, err := syslog.New(syslog.LOG_AUTHPRIV|syslog.LOG_NOTICE, "fstui"); err == nil {
		l.syslog = w
	}
	return l, nil
}

func checkOwned(path string, uid int, dir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case dir && !fi.IsDir(), !dir && !fi.Mode().IsRegular():
		return fmt.Errorf("audit log: %s is not a %s", path, map[bool]string{true: "directory", false: "regular file"}[dir])
	case !ok || int(st.Uid) != uid:
		return fmt.Errorf("audit log: %s is not owned by uid %d", path, uid)
	case fi.Mode().Perm()&fs.FileMode(0o022) != 0:
		return fmt.Errorf("audit log: %s %w", path, ErrUnsafe)
	}
	return nil
}

// Path returns the default audit log path.
func Path() string { return filepath.Join(DefaultDir, DefaultFile) }

// Record implements execute.Recorder.
func (l *Log) Record(ev execute.StepEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.broken != nil {
		return fmt.Errorf("an earlier write failed: %w", l.broken)
	}
	line, err := json.Marshal(l.record(ev))
	if err != nil {
		l.broken = fmt.Errorf("encoding a record: %w", err)
		return l.broken
	}
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		l.broken = fmt.Errorf("writing %s: %w", l.file.Name(), err)
		return l.broken
	}
	if l.syslog != nil {
		_ = l.syslog.Notice(string(line)) // best effort; the file is the record
	}
	return nil
}

func (l *Log) record(ev execute.StepEvent) Record {
	r := Record{
		Time: l.now().UTC().Format(time.RFC3339), UID: l.uid, SudoUser: l.sudoUser,
		Mode: ev.Mode.String(), PlanID: strconv.FormatUint(ev.Plan.ID, 16), Op: ev.Plan.Op, PlanSHA256: ev.Plan.SHA256,
		Step: ev.Step, Phase: string(ev.Phase), Argv: append([]string{string(ev.Argv.Tool)}, ev.Argv.Args()...),
	}
	if ev.Phase == execute.Outcome {
		code, ms := ev.ExitCode, ev.Duration.Milliseconds()
		r.ExitCode, r.DurationMS = &code, &ms
		s := ev.Stderr.Display()
		if len(s) > maxStderr {
			s = s[:maxStderr]
		}
		r.Stderr = s
	}
	return r
}

// WithoutSyslog stops copying records to syslog: the demo build keeps
// its records out of the host's logs.
func (l *Log) WithoutSyslog() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.syslog != nil {
		_ = l.syslog.Close()
		l.syslog = nil
	}
}

// Close closes the file and the syslog connection.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.syslog != nil {
		_ = l.syslog.Close()
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("closing the audit log: %w", err)
	}
	return nil
}

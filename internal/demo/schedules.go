// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Schedules is the demo's stand-in system for the Schedules tab: a
// temporary directory laid out like a systemd host, with OpenZFS's timer
// templates and a few schedules on, in which the demo carries out the
// schedule changes fstui plans. Nothing outside it is read or written.
type Schedules struct {
	Root string
	now  func() time.Time
}

// demoEnabled are the schedules on when the demo starts.
var demoEnabled = []string{
	"timers.target.wants/zfs-scrub-monthly@media.timer",
	"timers.target.wants/zfs-scrub-weekly@vault.timer",
	"timers.target.wants/zfs-trim-weekly@fast.timer",
}

// NewSchedules makes the demo's system in a new temporary directory;
// Close removes it.
func NewSchedules() (*Schedules, error) {
	root, err := os.MkdirTemp("", "fstui-demo-")
	if err != nil {
		return nil, fmt.Errorf("demo schedules: %w", err)
	}
	s := &Schedules{Root: root, now: time.Now}
	for _, d := range []string{"run/systemd/system", "etc/systemd/system/timers.target.wants", "etc/systemd/system/multi-user.target.wants", "usr/lib/systemd/system"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			return nil, errors.Join(fmt.Errorf("demo schedules: %w", err), s.Close())
		}
	}
	for _, kind := range []string{"scrub", "trim"} {
		for _, period := range []string{"weekly", "monthly"} {
			unit := "zfs-" + kind + "-" + period + "@.timer"
			body := "[Timer]\nOnCalendar=" + period + "\nPersistent=true\nUnit=zfs-" + kind + "@%i.service\n"
			if err := os.WriteFile(filepath.Join(root, "usr/lib/systemd/system", unit), []byte(body), 0o600); err != nil {
				return nil, errors.Join(fmt.Errorf("demo schedules: %w", err), s.Close())
			}
		}
	}
	for _, l := range demoEnabled {
		if err := os.Symlink("/usr/lib/systemd/system/template", filepath.Join(root, "etc/systemd/system", l)); err != nil {
			return nil, errors.Join(fmt.Errorf("demo schedules: %w", err), s.Close())
		}
	}
	return s, nil
}

// Close removes the demo's system.
func (s *Schedules) Close() error {
	if err := os.RemoveAll(s.Root); err != nil {
		return fmt.Errorf("removing the demo's schedules: %w", err)
	}
	return nil
}

// Read answers systemctl list-timers for the enabled timers, each next
// due a few days from now.
func (s *Schedules) Read(_ context.Context, a execute.Argv) (execute.Result, error) {
	if a.Tool != execute.Systemctl || len(a.Options) == 0 || a.Options[0] != "list-timers" {
		return execute.Result{}, fmt.Errorf("the demo answers only systemctl list-timers, not %s", a)
	}
	entries, _ := os.ReadDir(filepath.Join(s.Root, "etc/systemd/system/timers.target.wants")) // none is an empty list
	type timer struct {
		Next uint64 `json:"next"`
		Last uint64 `json:"last"`
		Unit string `json:"unit"`
	}
	out := []timer{}
	for _, e := range entries {
		h := fnv.New32a()
		_, _ = h.Write([]byte(e.Name()))
		next := s.now().Add(time.Duration(h.Sum32()%(7*24)) * time.Hour)
		out = append(out, timer{Next: uint64(next.UnixMicro()), Unit: e.Name()}) // a time after 1970
	}
	b, err := json.Marshal(out)
	if err != nil {
		return execute.Result{}, fmt.Errorf("demo timers: %w", err)
	}
	return execute.Result{Stdout: b}, nil
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_container && !fstui_nowrite

// These tests change the system they run on: they are built only with
// the fstui_container tag, for scripts/test-schedules-container.sh, which
// runs them as root inside throwaway containers (Gentoo with OpenRC,
// Debian with cron). Never run them on a real system.

package schedule

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
)

// fstuiInContainer is where the script puts fstui, owned by root.
const fstuiInContainer = "/usr/bin/fstui"

type stepLog []execute.StepEvent

func (s *stepLog) Record(e execute.StepEvent) error { *s = append(*s, e); return nil }

// apply builds the plan for id and when on this system and runs it the
// way the write module does: re-checked, then each step through the
// mutator.
func apply(t *testing.T, r Reader, id, when string) {
	t.Helper()
	tools, err := execute.ResolveOptional(nil, []execute.Tool{execute.Tee, execute.Chmod, execute.Mv, execute.Systemctl, execute.RcUpdate, execute.RcService})
	if err != nil {
		t.Fatal(err)
	}
	b := Builder{
		Reader: r,
		Binary: func() (string, error) { return execute.Trusted(fstuiInContainer) },
		Has:    func(tool execute.Tool) bool { _, ok := tools.Path(tool); return ok },
	}
	p, err := b.Set(t.Context(), id, when)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blocked) > 0 {
		t.Fatalf("%s %s blocked: %v", id, when, p.Blocked)
	}
	mode := new(execute.Switch)
	mode.Set(execute.Write)
	var log stepLog
	pl := mutate.Pipeline{Mode: mode, Mutator: execute.NewMutator(tools, mode, &log)}
	if _, err := pl.Run(t.Context(), p); err != nil {
		t.Fatalf("%s %s: %v; steps: %+v", id, when, err, log)
	}
}

func jobWhen(t *testing.T, r Reader, id string) string {
	t.Helper()
	jobs, err := r.Jobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return job(t, jobs, id).When
}

// OpenRC: fstui watch is installed, started under supervise-daemon,
// read back as on, then stopped and taken out of the runlevel.
func TestContainerOpenRCWatch(t *testing.T) {
	r := Reader{Host: Detect("/")}
	if !r.Host.OpenRC {
		t.Skip("not an OpenRC container")
	}
	apply(t, r, WatchID, On)
	if _, err := os.Lstat(runlevelLink); err != nil {
		t.Fatalf("not in the default runlevel: %v", err)
	}
	if w := jobWhen(t, r, WatchID); w != On {
		t.Errorf("watch reads back as %q", w)
	}
	// supervise-daemon runs fstui watch; without pools it fails and is
	// respawned, which is what the log shows.
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, _ := exec.CommandContext(context.Background(), "pgrep", "-f", "supervise-daemon fstui-watch").Output() //nolint:gosec // fixed arguments, test only
		logData, _ := os.ReadFile("/var/log/fstui-watch.log")
		if len(out) > 0 && strings.Contains(string(logData), "fstui watch") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("supervise-daemon is not running fstui watch; log:\n%s", logData)
		}
		time.Sleep(500 * time.Millisecond)
	}
	apply(t, r, WatchID, Off)
	if _, err := os.Lstat(runlevelLink); err == nil {
		t.Error("still in the default runlevel")
	}
	if w := jobWhen(t, r, WatchID); w != Off {
		t.Errorf("watch reads back as %q after off", w)
	}
}

// cron: a weekly task scheduled for the coming minute is written to
// /etc/cron.d/fstui, which the running cron daemon loads and runs.
func TestContainerCronTask(t *testing.T) {
	marker := "/tmp/fstui-cron-ran"
	at := time.Now().Add(time.Minute).Truncate(time.Minute)
	if time.Until(at) < 15*time.Second { // cron reads the file each minute
		at = at.Add(time.Minute)
	}
	s := config.Default().Schedules
	s.CronTime = at.Format("15:04")
	s.ScrubWeekday = config.Weekdays[at.Weekday()]
	r := Reader{
		Host:     Detect("/"),
		Settings: func() config.Schedules { return s },
		Tasks: func(context.Context) ([]Task, []Job, error) {
			return []Task{{Kind: "scrub", Target: "test", Command: []string{"/usr/bin/touch", marker}}}, nil, nil
		},
	}
	if r.Host.Systemd || !r.Host.Cron {
		t.Skip("not a cron container")
	}
	apply(t, r, "scrub:test", Weekly)
	if w := jobWhen(t, r, "scrub:test"); w != Weekly {
		t.Errorf("task reads back as %q", w)
	}
	deadline := at.Add(75 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(CronFile)
			t.Fatalf("cron did not run the entry by %s; %s:\n%s", deadline.Format("15:04:05"), CronFile, data)
		}
		time.Sleep(time.Second)
	}
	apply(t, r, "scrub:test", Off)
	if e := parseCron(mustRead(t, CronFile)); len(e) != 0 {
		t.Errorf("entries left after off: %+v", e)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

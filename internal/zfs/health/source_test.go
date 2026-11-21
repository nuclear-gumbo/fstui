// SPDX-License-Identifier: GPL-3.0-or-later

package health

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/problem"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// recorded answers zfs and zpool from the OpenZFS 2.3.9 recordings:
// reads from one scenario's files, the iostat stream from four pools.
type recorded struct {
	t        *testing.T
	scenario string
	fail     bool     // every read fails, as when zpool is missing
	asked    []string // the commands run, as "tool subcommand"
}

func (r *recorded) file(parts ...string) []byte {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "..", "testdata", "zfs", "2.3.9"}, parts...)...))
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

func (r *recorded) Read(_ context.Context, a execute.Argv) (execute.Result, error) {
	cmd := string(a.Tool) + " " + a.Options[0]
	r.asked = append(r.asked, cmd)
	if r.fail {
		return execute.Result{}, &execute.CommandError{Argv: a, ExitCode: 127}
	}
	files := map[string][]string{
		string(execute.Zpool) + " list":   {r.scenario, "zpool-list.json"},
		string(execute.Zpool) + " status": {r.scenario, "zpool-status.json"},
		string(execute.ZFS) + " list":     {r.scenario, "zfs-list.json"},
		string(execute.Zpool) + " events": {"events", "induced-v.txt"},
	}
	f, ok := files[cmd]
	if !ok {
		r.t.Fatalf("unexpected command %q", cmd)
	}
	return execute.Result{Stdout: r.file(f...)}, nil
}

func (r *recorded) Stream(ctx context.Context, a execute.Argv, line func([]byte)) error {
	r.asked = append(r.asked, string(a.Tool)+" "+a.Options[0])
	for l := range bytes.Lines(r.file("iostat", "four-pools.txt")) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line(bytes.TrimRight(l, "\n"))
	}
	return nil
}

func measureOf(ms []problem.Measure, subject string) (problem.Measure, bool) {
	for _, m := range ms {
		if m.Subject == subject {
			return m, true
		}
	}
	return problem.Measure{}, false
}

// Measures reads a degraded pool as alerts see it, and the system's
// subject; zpool iostat runs only when an alert needs the waits.
func TestMeasuresDegraded(t *testing.T) {
	r := &recorded{t: t, scenario: "degraded"}
	s := Source{Reader: zfs.NewReader(r)}
	ms, err := s.Measures(context.Background(), time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sys, ok := measureOf(ms, ""); !ok || sys.Numbers["versions_differ"] != 0 {
		t.Errorf("system subject = %+v, want versions_differ 0", sys)
	}
	m, ok := measureOf(ms, "fstuitest_g_degraded")
	if !ok {
		t.Fatalf("no measure of the pool in %+v", ms)
	}
	if m.States["health"] != "DEGRADED" || m.Numbers["devices_down"] != 1 {
		t.Errorf("health %q, devices_down %v; want DEGRADED, 1", m.States["health"], m.Numbers["devices_down"])
	}
	if !strings.Contains(m.Details["devices_down"], "OFFLINE") {
		t.Errorf("detail %q does not name the offline device", m.Details["devices_down"])
	}
	for _, k := range []string{"capacity", "free", "data_errors", "device_errors", "resilvering"} {
		if _, ok := m.Numbers[k]; !ok {
			t.Errorf("%s not measured", k)
		}
	}
	for _, c := range r.asked {
		if strings.HasSuffix(c, " iostat") {
			t.Error("zpool iostat ran although no alert needs the waits")
		}
	}

	r.asked = nil
	if _, err := s.Measures(context.Background(), time.Now(), map[string]bool{"read_wait": true}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(r.asked[len(r.asked)-1], " iostat") {
		t.Errorf("commands %v: the waits were not read", r.asked)
	}
}

// A failed read is an error for fstui check, not an empty set of pools,
// which would clear every open alert.
func TestMeasuresReadFails(t *testing.T) {
	s := Source{Reader: zfs.NewReader(&recorded{t: t, fail: true})}
	ms, err := s.Measures(context.Background(), time.Now(), nil)
	var ce *execute.CommandError
	if !errors.As(err, &ce) || ms != nil {
		t.Errorf("Measures = %v, %v; want nil and the command's error", ms, err)
	}
}

// The waits come from the first interval of zpool iostat, in
// milliseconds; a pool without I/O, or one not measured, gets none.
func TestAddWaits(t *testing.T) {
	ms := []problem.Measure{
		{Subject: "fstuitest_demo_roles", Numbers: map[string]float64{}},
		{Subject: "fstuitest_demo_backup", Numbers: map[string]float64{}},
		{Subject: "elsewhere", Numbers: map[string]float64{}},
	}
	addWaits(context.Background(), zfs.NewReader(&recorded{t: t}), ms)
	roles := ms[0].Numbers
	if roles["read_wait"] != 0.294912 || roles["write_wait"] != 3.950308 {
		t.Errorf("roles waits = %v, %v; want 0.294912, 3.950308 ms", roles["read_wait"], roles["write_wait"])
	}
	for _, m := range ms[1:] {
		if _, ok := m.Numbers["read_wait"]; ok {
			t.Errorf("%s got a read wait: %v", m.Subject, m.Numbers)
		}
	}
}

func TestSourceEvents(t *testing.T) {
	evs, _, err := Source{Reader: zfs.NewReader(&recorded{t: t})}.Events(context.Background())
	if err != nil || len(evs) == 0 {
		t.Errorf("Events = %d events, %v; want the recorded ones", len(evs), err)
	}
}

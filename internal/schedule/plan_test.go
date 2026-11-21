// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package schedule

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

func builder(r Reader) Builder {
	return Builder{
		Reader: r,
		Binary: func() (string, error) { return "/usr/bin/fstui", nil },
		Has:    func(execute.Tool) bool { return true },
	}
}

// steps renders a plan's steps one per line.
func steps(t *testing.T, b Builder, id, when string) (string, []string, string) {
	t.Helper()
	p, err := b.Set(t.Context(), id, when)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, s := range p.Steps {
		lines = append(lines, string(s.Tool)+" "+strings.Join(s.Args(), " "))
	}
	return strings.Join(lines, "\n"), p.Blocked, p.Preview.Output.Raw()
}

// Moving a scrub from weekly to monthly stops one OpenZFS timer and
// starts the other; no unit file is written.
func TestPlanSystemdTask(t *testing.T) {
	r := systemdHost(t, nil, timersWants+`/zfs-scrub-weekly@my\x2dpool.timer`)
	got, blocked, _ := steps(t, builder(r), "scrub:my-pool", Monthly)
	want := "systemctl disable --now -- zfs-scrub-weekly@my\\x2dpool.timer\nsystemctl enable --now -- zfs-scrub-monthly@my\\x2dpool.timer"
	if got != want || blocked != nil {
		t.Errorf("steps:\n%s\nwant:\n%s\nblocked %v", got, want, blocked)
	}
	if _, err := builder(r).Set(t.Context(), "trim:my-pool", Monthly); err == nil {
		t.Error("a period without its template was planned")
	}
	p, err := builder(r).Set(t.Context(), "scrub:my-pool", Weekly)
	if err != nil || len(p.Blocked) != 1 || !strings.Contains(p.Blocked[0], "already") {
		t.Errorf("no change: %v %+v", err, p)
	}
}

// Turning fstui check on writes both units in full, reloads systemd and
// enables the timer; watch is refused while check runs, and a binary
// others could replace blocks the plan.
func TestPlanSystemdCheckAndWatch(t *testing.T) {
	r := systemdHost(t, nil)
	b := builder(r)
	got, blocked, shown := steps(t, b, CheckID, On)
	for _, want := range []string{
		"tee -- " + UnitDir + "/." + CheckService + ".fstui-new", "chmod 0644 -- ", "mv -- ",
		"systemctl daemon-reload", "systemctl enable --now -- " + CheckTimer,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("steps lack %q:\n%s", want, got)
		}
	}
	if blocked != nil || !strings.Contains(shown, "ExecStart=/usr/bin/fstui check") || !strings.Contains(shown, "OnUnitActiveSec=15m") {
		t.Errorf("blocked %v; shown:\n%s", blocked, shown)
	}

	r = systemdHost(t, nil, timersWants+"/"+CheckTimer)
	if _, blocked, _ := steps(t, builder(r), WatchID, On); len(blocked) != 1 || !strings.Contains(blocked[0], "turn it off first") {
		t.Errorf("watch beside check: %v", blocked)
	}

	b = builder(systemdHost(t, nil))
	b.Binary = func() (string, error) { return "", errors.New("/home/u/fstui is owned by uid 1000") }
	if _, blocked, _ := steps(t, b, WatchID, On); len(blocked) != 1 || !strings.Contains(blocked[0], "others could replace") {
		t.Errorf("untrusted binary: %v", blocked)
	}
}

// cron: a task and fstui check go into fstui's file; Off takes the entry
// out again.
func TestPlanCron(t *testing.T) {
	root := fakeRoot(t, map[string]string{"/usr/sbin/cron": "", "/etc/cron.d/": ""})
	r := Reader{Host: Detect(root), Tasks: zfsTasks("tank")}
	_, blocked, shown := steps(t, builder(r), "scrub:tank", Weekly)
	if blocked != nil || !strings.Contains(shown, "0 3 * * 0 root /usr/sbin/zpool scrub tank") {
		t.Errorf("blocked %v; shown:\n%s", blocked, shown)
	}
	// Write the file as the plan would, then turn the scrub off.
	if err := os.WriteFile(filepath.Join(root, CronFile), []byte(cronFile(map[string]cronEntry{"scrub:tank": {when: Weekly, line: "x"}})), 0o644); err != nil { // a test file
		t.Fatal(err)
	}
	_, _, shown = steps(t, builder(r), "scrub:tank", Off)
	if strings.Contains(shown, "scrub:tank") {
		t.Errorf("off kept the entry:\n%s", shown)
	}
}

// OpenRC: fstui watch gets an init script and joins the default
// runlevel; the re-check notices the script changing.
func TestPlanOpenRC(t *testing.T) {
	root := fakeRoot(t, map[string]string{"/run/openrc/": ""})
	b := builder(Reader{Host: Detect(root)})
	got, blocked, shown := steps(t, b, WatchID, On)
	if blocked != nil || !strings.Contains(got, "rc-update add fstui-watch default") || !strings.Contains(got, "rc-service fstui-watch start") ||
		!strings.Contains(got, "chmod 0755") || !strings.Contains(shown, "command='/usr/bin/fstui'") {
		t.Errorf("blocked %v; steps:\n%s\nshown:\n%s", blocked, got, shown)
	}
	p, err := b.Set(t.Context(), WatchID, On)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "/etc/init.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, InitScript), []byte("someone's\n"), 0o755); err != nil { // a test file
		t.Fatal(err)
	}
	now, err := p.Recheck(t.Context())
	if err != nil || now[InitScript] == p.Baseline[InitScript] {
		t.Errorf("re-check missed the new script: %v %v", now, p.Baseline)
	}
}

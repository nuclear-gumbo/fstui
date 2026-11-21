// SPDX-License-Identifier: GPL-3.0-or-later

package schedule

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// fakeRoot builds a system under a temporary directory: files by path,
// and symlinks for enabled units.
func fakeRoot(t *testing.T, files map[string]string, links ...string) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, p)
		dir := filepath.Dir(full)
		if strings.HasSuffix(p, "/") {
			dir = full
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(p, "/") {
			continue
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil { // a test file
			t.Fatal(err)
		}
	}
	for _, l := range links {
		full := filepath.Join(root, l)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/dev/null", full); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// timers answers systemctl list-timers.
type timers string

func (t timers) Read(context.Context, execute.Argv) (execute.Result, error) {
	return execute.Result{Stdout: []byte(t)}, nil
}

func zfsTasks(pools ...string) func(context.Context) ([]Task, []Job, error) {
	return func(context.Context) ([]Task, []Job, error) {
		var out []Task
		for _, p := range pools {
			out = append(out,
				Task{Kind: "scrub", Target: p, Templates: map[string]string{Weekly: "zfs-scrub-weekly@.timer", Monthly: "zfs-scrub-monthly@.timer"}, Command: []string{"/usr/sbin/zpool", "scrub", p}},
				Task{Kind: "trim", Target: p, Templates: map[string]string{Weekly: "zfs-trim-weekly@.timer", Monthly: "zfs-trim-monthly@.timer"}, Command: []string{"/usr/sbin/zpool", "trim", p}})
		}
		return out, []Job{{What: "scrub every pool", When: Monthly, By: "Debian's cron job"}}, nil
	}
}

var templates = map[string]string{
	"/run/systemd/system/":                             "",
	"/usr/lib/systemd/system/zfs-scrub-weekly@.timer":  "",
	"/usr/lib/systemd/system/zfs-scrub-monthly@.timer": "",
	"/usr/lib/systemd/system/zfs-trim-weekly@.timer":   "",
}

func systemdHost(t *testing.T, extra map[string]string, links ...string) Reader {
	t.Helper()
	files := map[string]string{}
	for k, v := range templates {
		files[k] = v
	}
	for k, v := range extra {
		files[k] = v
	}
	root := fakeRoot(t, files, links...)
	return Reader{
		Host:  Detect(root),
		Run:   timers(`[{"next":1795054454318647,"last":0,"unit":"zfs-scrub-weekly@my\\x2dpool.timer"}]`),
		Tasks: zfsTasks("my-pool"),
	}
}

func job(t *testing.T, jobs []Job, id string) Job {
	t.Helper()
	for _, j := range jobs {
		if j.ID == id {
			return j
		}
	}
	t.Fatalf("no job %s in %+v", id, jobs)
	return Job{}
}

// systemd: an enabled OpenZFS timer instance is read with its next run;
// only the periods whose template is installed are offered.
func TestJobsSystemd(t *testing.T) {
	r := systemdHost(t, map[string]string{UnitDir + "/" + CheckTimer: "[Timer]\nOnUnitActiveSec=30m\n"},
		timersWants+`/zfs-scrub-weekly@my\x2dpool.timer`, timersWants+"/"+CheckTimer)
	jobs, err := r.Jobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s := job(t, jobs, "scrub:my-pool")
	if s.When != Weekly || s.Next.IsZero() || !strings.Contains(s.By, `zfs-scrub-weekly@my\x2dpool.timer`) {
		t.Errorf("scrub = %+v", s)
	}
	if tr := job(t, jobs, "trim:my-pool"); tr.When != Off || strings.Join(tr.Choices, ",") != "off,weekly" {
		t.Errorf("trim = %+v", tr)
	}
	if c := job(t, jobs, CheckID); c.When != "every 30m" || !strings.Contains(c.Note, "15m") {
		t.Errorf("check = %+v", c)
	}
	if w := job(t, jobs, WatchID); w.When != Off || len(w.Choices) != 2 {
		t.Errorf("watch = %+v", w)
	}
	if last := jobs[len(jobs)-1]; last.ID != "" || last.Choices != nil {
		t.Errorf("the backend's other schedule = %+v", last)
	}
}

func TestInstance(t *testing.T) {
	for in, want := range map[string]string{"tank": "z@tank.timer", "my-pool": `z@my\x2dpool.timer`, "a.b:c_d": "z@a.b:c_d.timer", ".x": `z@\x2ex.timer`} {
		if got := Instance("z@.timer", in); got != want {
			t.Errorf("Instance(%q) = %q, want %q", in, got, want)
		}
	}
}

// Without systemd, cron runs tasks and fstui check from fstui's file; the
// file reads back what was written.
func TestCronFile(t *testing.T) {
	task := Task{Kind: "trim", Target: "tank", Command: []string{"/usr/sbin/zpool", "trim", "tank"}}
	s := config.Default().Schedules
	entries := map[string]cronEntry{
		"trim:tank": {when: Weekly, line: taskCron(task, Weekly, s)},
		CheckID:     {when: "every 15m", line: checkCron("/usr/bin/fstui", "15m")},
	}
	data := cronFile(entries)
	for _, want := range []string{"0 3 * * 3 root /usr/sbin/zpool trim tank\n", "*/15 * * * * root /usr/bin/fstui check >/dev/null\n", "# fstui: trim:tank weekly\n"} {
		if !strings.Contains(data, want) {
			t.Errorf("cron file lacks %q:\n%s", want, data)
		}
	}
	back := parseCron([]byte(data + "* * * * * root someone else's line\n"))
	if len(back) != 2 || back["trim:tank"] != entries["trim:tank"] {
		t.Errorf("parseCron = %+v", back)
	}
	if got := taskCron(Task{Kind: "scrub", Command: []string{"/x"}}, Monthly, s); got != "0 3 1 * * root /x" {
		t.Errorf("monthly scrub = %q", got)
	}
	if got := cronCommand([]string{"/a b", "50%"}); got != `'/a b' '50\%'` {
		t.Errorf("cronCommand = %q", got)
	}
}

func TestJobsCronAndNothing(t *testing.T) {
	root := fakeRoot(t, map[string]string{
		"/usr/sbin/cron": "", "/etc/cron.d/": "",
		CronFile: cronFile(map[string]cronEntry{"scrub:tank": {when: Monthly, line: "x"}}),
	})
	r := Reader{Host: Detect(root), Tasks: zfsTasks("tank")}
	jobs, err := r.Jobs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if s := job(t, jobs, "scrub:tank"); s.When != Monthly || s.By != CronFile {
		t.Errorf("scrub = %+v", s)
	}
	if w := job(t, jobs, WatchID); w.Choices != nil || w.Note == "" {
		t.Errorf("watch without systemd or OpenRC = %+v", w)
	}
	r = Reader{Host: Detect(t.TempDir()), Tasks: zfsTasks("tank")}
	jobs, _ = r.Jobs(t.Context())
	if s := job(t, jobs, "scrub:tank"); s.Choices != nil || s.Note != noScheduler {
		t.Errorf("no scheduler: %+v", s)
	}
}

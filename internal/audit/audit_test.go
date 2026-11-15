// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

func open(t *testing.T) (*Log, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fstui")
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.syslog = nil // keep test records out of the host's journal
	l.now = func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.FixedZone("x", 3600)) }
	t.Cleanup(func() { _ = l.Close() })
	return l, filepath.Join(dir, DefaultFile)
}

func lines(t *testing.T, path string) []Record {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		out = append(out, r)
	}
	return out
}

func TestRecordsAttemptAndResult(t *testing.T) {
	l, path := open(t)
	a := execute.Argv{Tool: execute.ZFS, Options: []string{"snapshot"}, Operands: []string{"tank/a@x"}}
	ev := execute.StepEvent{Mode: execute.Write, Plan: execute.PlanRef{ID: 255, Op: "snapshot-create"}, Step: 1, Phase: execute.Attempt, Argv: a}
	if err := l.Record(ev); err != nil {
		t.Fatal(err)
	}
	ev.Phase, ev.ExitCode, ev.Duration = execute.Outcome, 1, 1500*time.Millisecond
	ev.Stderr = termtext.New("cannot create snapshot \x1b[2J" + strings.Repeat("x", 5000))
	if err := l.Record(ev); err != nil {
		t.Fatal(err)
	}
	rs := lines(t, path)
	if len(rs) != 2 {
		t.Fatalf("%d records", len(rs))
	}
	at, res := rs[0], rs[1]
	if at.Time != "2026-11-14T11:00:00Z" || at.Phase != "attempt" || at.PlanID != "ff" || at.Op != "snapshot-create" ||
		at.Mode != "write" || strings.Join(at.Argv, " ") != "zfs snapshot -- tank/a@x" || at.ExitCode != nil {
		t.Errorf("attempt = %+v", at)
	}
	if res.ExitCode == nil || *res.ExitCode != 1 || *res.DurationMS != 1500 || len(res.Stderr) != maxStderr ||
		strings.ContainsRune(res.Stderr, 0x1b) {
		t.Errorf("result = %+v", res)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode())
	}
}

func TestRefusesAfterAFailedWrite(t *testing.T) {
	l, _ := open(t)
	_ = l.file.Close() // every write now fails
	ev := execute.StepEvent{Mode: execute.Write, Step: 1, Phase: execute.Attempt}
	if err := l.Record(ev); err == nil {
		t.Fatal("Record on a closed file succeeded")
	}
	l.file, _ = os.CreateTemp(t.TempDir(), "x") // even a working file again
	if err := l.Record(ev); err == nil {
		t.Fatal("Record after a failure succeeded")
	}
}

func TestOpenRefusesUnsafePlaces(t *testing.T) {
	dir := t.TempDir()
	wide := filepath.Join(dir, "wide")
	if err := os.Mkdir(wide, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(wide, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(wide); !errors.Is(err, ErrUnsafe) {
		t.Errorf("world-writable dir: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(link, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(link, DefaultFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil {
		t.Error("audit.log as a symlink was accepted")
	}
}

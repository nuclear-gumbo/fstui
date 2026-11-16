// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeScript stands in for zfs: its first argument selects a behaviour.
// Read runs it with fstui's fixed PATH, which on NixOS and in the Nix build
// sandbox contains no coreutils, so it uses shell builtins only; the one
// external program, sleep, is substituted as an absolute path.
const fakeScript = `#!/bin/sh
case "$1" in
args) shift; for a in "$@"; do printf '%%s\n' "$a"; done ;;
env)
	[ -n "${PATH+x}" ] && printf 'PATH=%%s\n' "$PATH"
	[ -n "${LC_ALL+x}" ] && printf 'LC_ALL=%%s\n' "$LC_ALL"
	[ -n "${HOME+x}" ] && printf 'HOME=%%s\n' "$HOME"
	[ -n "${FSTUI_CANARY+x}" ] && printf 'FSTUI_CANARY=%%s\n' "$FSTUI_CANARY"
	[ -n "${ZPOOL_IMPORT_PATH+x}" ] && printf 'ZPOOL_IMPORT_PATH=%%s\n' "$ZPOOL_IMPORT_PATH"
	exit 0 ;;
fail) printf 'cannot open %%s: \033[2Jboom\n' "$3" >&2; exit 3 ;;
sleep) %[1]s 30 & wait ;;
stream) printf 'one\ntwo\n'; %[1]s 30 & wait ;;
streamfail) printf 'partial\n'; printf 'no pools\n' >&2; exit 2 ;;
stdin) read -r l; printf 'got %%s\n' "$l" ;;
mark) printf x > "$2" ;;
made) printf 'made, but it may only be mounted by root\n' >&2; exit 1 ;;
esac
`

const fake Tool = "fake"

func fakeRunner(t *testing.T) *Runner {
	t.Helper()
	tr := newTree(t)
	path := filepath.Join(tr.bin, string(fake))
	// "sleep" stays unresolved when the test host has none; only the
	// timeout test uses it, and it skips in that case.
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		sleep = "sleep"
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(fakeScript, sleep)), 0o600); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, path, 0o755)
	tools, err := tr.r.resolve([]Tool{fake})
	if err != nil {
		t.Fatal(err)
	}
	return NewRunner(tools)
}

func TestArgsPutOperandsAfterDoubleDash(t *testing.T) {
	a := Argv{Tool: ZFS, Options: []string{"destroy", "-nvp"}, Operands: []string{"tank/-r"}}
	got := strings.Join(a.Args(), " ")
	if want := "destroy -nvp -- tank/-r"; got != want {
		t.Fatalf("Args() = %q, want %q", got, want)
	}
}

func TestArgsWithoutOperandsHaveNoDoubleDash(t *testing.T) {
	a := Argv{Tool: Zpool, Options: []string{"list", "-j"}}
	if got := strings.Join(a.Args(), " "); got != "list -j" {
		t.Fatalf("Args() = %q, want %q", got, "list -j")
	}
}

func TestArgvStringQuotesAndSanitises(t *testing.T) {
	a := Argv{Tool: ZFS, Options: []string{"get"}, Operands: []string{"tank/with space", "tag\x1b[1m"}}
	got := a.String()
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("String() contains ESC: %q", got)
	}
	if want := `zfs get -- "tank/with space" tag\x1b[1m`; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestReadPassesOperandsUnparsed runs a real process and checks that an
// operand starting with "-" arrives after "--", where getopt cannot take it
// for an option.
func TestReadPassesOperandsUnparsed(t *testing.T) {
	r := fakeRunner(t)
	res, err := r.Read(context.Background(), Argv{Tool: fake, Options: []string{"args", "-H"}, Operands: []string{"-r", "tank/a b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(res.Stdout), "-H\n--\n-r\ntank/a b\n"; got != want {
		t.Fatalf("child saw %q, want %q", got, want)
	}
}

func TestReadUsesOnlyTheFixedEnvironment(t *testing.T) {
	t.Setenv("FSTUI_CANARY", "leaked")
	t.Setenv("ZPOOL_IMPORT_PATH", "/tmp/evil")
	r := fakeRunner(t)
	res, err := r.Read(context.Background(), Argv{Tool: fake, Options: []string{"env"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		got[k] = v
	}
	if got["PATH"] != "/usr/sbin:/usr/bin:/sbin:/bin" {
		t.Errorf("PATH = %q", got["PATH"])
	}
	if got["LC_ALL"] != "C" {
		t.Errorf("LC_ALL = %q", got["LC_ALL"])
	}
	for _, k := range []string{"FSTUI_CANARY", "ZPOOL_IMPORT_PATH", "HOME"} {
		if v, ok := got[k]; ok {
			t.Errorf("inherited %s=%q", k, v)
		}
	}
}

func TestReadReportsFailureWithSanitisedStderr(t *testing.T) {
	r := fakeRunner(t)
	a := Argv{Tool: fake, Options: []string{"fail"}, Operands: []string{"tank/x"}}
	_, err := r.Read(context.Background(), a)
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *CommandError", err)
	}
	if ce.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", ce.ExitCode)
	}
	if !strings.ContainsRune(ce.Stderr.Raw(), 0x1b) {
		t.Errorf("Stderr.Raw() lost the original bytes: %q", ce.Stderr.Raw())
	}
	msg := err.Error()
	if strings.ContainsRune(msg, 0x1b) {
		t.Fatalf("Error() contains ESC: %q", msg)
	}
	if !strings.Contains(msg, `cannot open tank/x: \x1b[2Jboom`) || !strings.Contains(msg, "status 3") {
		t.Fatalf("Error() = %q, want stderr and exit status", msg)
	}
}

func TestReadTimesOut(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary on this host")
	}
	r := fakeRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Read(ctx, Argv{Tool: fake, Options: []string{"sleep"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	// The script's backgrounded sleep holds the pipes open after the shell
	// is killed; WaitDelay must bound the wait for it.
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Read returned after %s, want shortly after the deadline", d)
	}
}

func TestReadRefusesUnresolvedTool(t *testing.T) {
	r := fakeRunner(t)
	_, err := r.Read(context.Background(), Argv{Tool: Zpool, Options: []string{"list"}})
	wantErrContaining(t, err, "not resolved")
}

// Stream hands over lines as they arrive and returns nil when cancelled,
// although the command would have kept running.
func TestStreamDeliversLinesUntilCancelled(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary on this host")
	}
	r := fakeRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	var got []string
	done := make(chan error, 1)
	go func() {
		done <- r.Stream(ctx, Argv{Tool: fake, Options: []string{"stream"}}, func(b []byte) {
			got = append(got, string(b))
			if len(got) == 2 {
				cancel()
			}
		})
	}()
	select {
	case err := <-done:
		if err != nil || strings.Join(got, ",") != "one,two" {
			t.Fatalf("err %v, lines %q", err, got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stream did not return after cancel")
	}
}

func TestStreamReportsExit(t *testing.T) {
	r := fakeRunner(t)
	var got []string
	err := r.Stream(context.Background(), Argv{Tool: fake, Options: []string{"streamfail"}}, func(b []byte) { got = append(got, string(b)) })
	var ce *CommandError
	if !errors.As(err, &ce) || ce.ExitCode != 2 || !strings.Contains(ce.Stderr.Display(), "no pools") || len(got) != 1 {
		t.Fatalf("err %v, lines %q", err, got)
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := &limitedBuffer{max: 4}
	if n, err := b.Write([]byte("abcdef")); n != 6 || err != nil || b.String() != "abcd" {
		t.Fatalf("n %d err %v kept %q", n, err, b.String())
	}
}

// Output past the bound is not kept, and the buffer says so; Read then
// fails rather than parse a cut listing.
func TestLimitedBufferMarksOverflow(t *testing.T) {
	b := &limitedBuffer{max: 4}
	_, _ = b.Write([]byte("ab"))
	if b.over {
		t.Fatal("over before the bound")
	}
	_, _ = b.Write([]byte("cdef"))
	if !b.over || b.String() != "abcd" {
		t.Errorf("kept %q, over %v", b.String(), b.over)
	}
}

// Read applies the same operand check as a change.
func TestReadRefusesADashOperandWithoutSeparator(t *testing.T) {
	r := NewRunner(&Tools{paths: map[Tool]string{}})
	_, err := r.Read(t.Context(), Argv{Tool: ZFS, Options: []string{"promote"}, Operands: []string{"-x"}, NoSeparator: true})
	if !errors.Is(err, ErrOperandOption) {
		t.Errorf("err = %v", err)
	}
}

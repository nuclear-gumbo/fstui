// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testHost(paths map[Tool]string) Host {
	return Host{
		Destination: "root@pve1", ControlDir: "/run/user/1000/fstui/ssh",
		ConnectTimeout: 10 * time.Second, AliveInterval: 15 * time.Second, Persist: time.Minute,
		Paths: paths,
	}
}

// hostile are names ZFS allows in some form or a user could type, each
// meaning something to a shell.
var hostile = []string{
	"tank/a b", "it's", `say "hi"`, "$HOME", "`id`", "$(id)", `back\slash`, "new\nline",
	"-leading", "semi;colon", "*", "~root", "a&&b", "pipe|x", "<in", "tab\there", "ñandú", "",
}

// The remote string, given to a real POSIX shell as ssh gives it to the
// login shell, runs the tool with exactly the words fstui built.
func TestWrapRoundTrip(t *testing.T) {
	printf, err1 := exec.LookPath("/usr/bin/printf")
	envPath, err2 := exec.LookPath("/usr/bin/env")
	if err1 != nil || err2 != nil {
		t.Skip("needs /usr/bin/printf and /usr/bin/env, absent in the Nix build sandbox")
	}
	h := testHost(map[Tool]string{ZFS: printf, RemoteEnv: envPath})
	w, err := h.Wrap(Argv{Tool: ZFS, Options: []string{`%s\0`}, Operands: hostile})
	if err != nil {
		t.Fatal(err)
	}
	if w.Operands[0] != h.Destination {
		t.Fatalf("destination operand = %q", w.Operands[0])
	}
	remote := w.Operands[1]
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/dash", "/usr/bin/zsh"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		out, err := exec.Command(shell, "-c", remote).Output()
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		if want := append([]string{"--"}, hostile...); !slices.Equal(got, want) {
			t.Errorf("%s gave\n%q\nwant\n%q", shell, got, want)
		}
	}
}

// The remote command runs under env -i with fstui's fixed environment,
// by the absolute path the probe checked.
func TestWrapCommandLine(t *testing.T) {
	h := testHost(map[Tool]string{ZFS: "/usr/sbin/zfs", RemoteEnv: "/usr/bin/env"})
	w, err := h.Wrap(Argv{Tool: ZFS, Options: []string{"list", "-j"}, Operands: []string{"tank/a b"}, Stdin: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-T", "-e", "none", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ControlMaster=auto",
		"-o", "ControlPath=/run/user/1000/fstui/ssh/%C", "-o", "ControlPersist=60",
		"--", "root@pve1",
		"/usr/bin/env -i 'PATH=/usr/sbin:/usr/bin:/sbin:/bin' LC_ALL=C /usr/sbin/zfs list -j -- 'tank/a b'",
	}
	if w.Tool != SSH || !slices.Equal(w.Args(), want) {
		t.Errorf("got %s %q\nwant ssh %q", w.Tool, w.Args(), want)
	}
	if !w.Stdin {
		t.Error("the passphrase step lost its standard input")
	}
}

func TestWrapRefuses(t *testing.T) {
	full := map[Tool]string{ZFS: "/usr/sbin/zfs", RemoteEnv: "/usr/bin/env"}
	for name, c := range map[string]struct {
		host Host
		a    Argv
		want error
	}{
		"tool not probed": {testHost(map[Tool]string{RemoteEnv: "/usr/bin/env"}), Argv{Tool: ZFS}, ErrRemoteTool},
		"env not probed":  {testHost(map[Tool]string{ZFS: "/usr/sbin/zfs"}), Argv{Tool: ZFS}, ErrRemoteTool},
		"operand option":  {testHost(full), Argv{Tool: ZFS, Options: []string{"promote"}, Operands: []string{"-x"}, NoSeparator: true}, ErrOperandOption},
		"destination":     {Host{Destination: "-oProxyCommand=x", ControlDir: "/run/x", Paths: full}, Argv{Tool: ZFS}, ErrDestination},
		"control dir":     {Host{Destination: "pve1", ControlDir: "/home/a b", Paths: full}, Argv{Tool: ZFS}, ErrOptionPath},
	} {
		if _, err := c.host.Wrap(c.a); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

func TestCheckDestination(t *testing.T) {
	for _, d := range []string{"pve1", "root@pve1", "root@10.10.1.101", "fe80::1", "root@fe80::1", "node-2.example.org", "my_alias"} {
		if err := CheckDestination(d); err != nil {
			t.Errorf("%q refused: %v", d, err)
		}
	}
	for _, d := range []string{
		"", "-oProxyCommand=sh", "-p", "pve1 -o x", "host;id", "a@b@c", "@pve1", "root@", "$(id)",
		"pve1\n", "pve`id`", "h%h", "~root", "pve1/x", strings.Repeat("a", maxDestination+1),
	} {
		if err := CheckDestination(d); !errors.Is(err, ErrDestination) {
			t.Errorf("%q accepted", d)
		}
	}
}

func TestCheckControlDir(t *testing.T) {
	if err := checkControlDir("/run/user/1000/fstui/ssh"); err != nil {
		t.Error(err)
	}
	for _, d := range []string{"run/fstui", "/tmp/%h", "~/ssh", "/home/a b", `/x"y`, "/" + strings.Repeat("d", maxControlDir)} {
		if err := checkControlDir(d); !errors.Is(err, ErrOptionPath) {
			t.Errorf("%q accepted", d)
		}
	}
}

// ssh reads 0 as no timeout, so nothing below a second is passed as 0.
func TestSecondsAtLeastOne(t *testing.T) {
	if got := seconds(300 * time.Millisecond); got != "1" {
		t.Errorf("seconds(300ms) = %s", got)
	}
}

func TestExit(t *testing.T) {
	a, err := testHost(nil).Exit()
	if err != nil {
		t.Fatal(err)
	}
	args := a.Args()
	if tail := args[len(args)-4:]; !slices.Equal(tail, []string{"-O", "exit", "--", "root@pve1"}) {
		t.Errorf("args end %q", tail)
	}
}

// listen makes a Unix socket at path without the net package, which
// only internal/notify may import.
func listen(t *testing.T, path string) {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Close(fd); err != nil {
			t.Error(err)
		}
	})
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
}

func TestSSHEnvironment(t *testing.T) {
	dir := t.TempDir()
	sock, file := filepath.Join(dir, "agent"), filepath.Join(dir, "file")
	listen(t, sock)
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	uid := os.Getuid()
	home := func(int) (string, error) { return "/home/u", nil }
	for name, c := range map[string]struct {
		uid   int
		agent string
		want  []string
	}{
		"own agent":       {uid, sock, []string{"HOME=/home/u", "SSH_AUTH_SOCK=" + sock}},
		"no agent":        {uid, "", []string{"HOME=/home/u"}},
		"another's agent": {uid + 1, sock, []string{"HOME=/home/u"}},
		"not a socket":    {uid, file, []string{"HOME=/home/u"}},
		"relative":        {uid, "agent", []string{"HOME=/home/u"}},
	} {
		if got := sshEnvironment(c.uid, c.agent, home); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	noHome := func(int) (string, error) { return "", errors.New("no entry") }
	if got := sshEnvironment(uid, "", noHome); len(got) != 0 {
		t.Errorf("without a password entry: %q", got)
	}
}

// Only ssh gets more than the fixed environment, and adding it never
// changes the environment other commands get.
func TestEnvFor(t *testing.T) {
	if !slices.Equal(envFor(ZFS), env) {
		t.Errorf("zfs env = %q", envFor(ZFS))
	}
	before := slices.Clone(env)
	s := envFor(SSH)
	if !slices.Equal(s[:len(env)], env) || !slices.Equal(env, before) {
		t.Errorf("ssh env %q changed the fixed one %q", s, env)
	}
}

// A Proxmox node is reached as the cluster reaches it; the alias and
// the file are refused if ssh could read them as something else.
func TestKeyOptions(t *testing.T) {
	h := testHost(map[Tool]string{ZFS: "/usr/sbin/zfs", RemoteEnv: "/usr/bin/env"})
	h.Destination, h.HostKeyAlias, h.KnownHostsFile = "root@192.0.2.12", "pve2", "/etc/pve/nodes/pve2/ssh_known_hosts"
	w, err := h.Wrap(Argv{Tool: ZFS})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(w.Args(), " ")
	if !strings.Contains(args, "-o HostKeyAlias=pve2 -o UserKnownHostsFile=/etc/pve/nodes/pve2/ssh_known_hosts -o GlobalKnownHostsFile=none -- root@192.0.2.12") {
		t.Errorf("args %s", args)
	}
	for _, bad := range []Host{
		{HostKeyAlias: "-x"},
		{HostKeyAlias: "a@b"},
		{HostKeyAlias: "a b"},
		{KnownHostsFile: "relative"},
		{KnownHostsFile: "/a b"},
		{KnownHostsFile: "/x/%h"},
	} {
		b := testHost(nil)
		b.HostKeyAlias, b.KnownHostsFile = bad.HostKeyAlias, bad.KnownHostsFile
		if _, err := b.Exit(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

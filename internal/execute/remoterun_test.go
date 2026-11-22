// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSSH does with its arguments what ssh does: options up to --, the
// destination, then the remote command for the login shell, here the
// local /bin/sh. Destination "down" fails as ssh does when the host
// refuses the connection.
const fakeSSH = `#!/bin/sh
while [ "$1" != "--" ]; do shift; done
shift
if [ "$1" = down ]; then
	printf 'ssh: connect to host down port 22: Connection refused\n' >&2
	exit 255
fi
shift
exec /bin/sh -c "$1"
`

// fakeRemote is a Remote through fakeSSH, probed, so that its tools are
// this machine's, found the way a remote host's are.
func fakeRemote(t *testing.T, dest string) *Remote {
	t.Helper()
	for _, p := range []string{"/usr/bin/env", "/usr/bin/printf", "/bin/sh"} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("needs %s, absent in the Nix build sandbox", p)
		}
	}
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte(fakeSSH), 0o600); err != nil {
		t.Fatal(err)
	}
	mustChmod(t, path, 0o755)
	r := NewRemote(NewRunner(&Tools{paths: map[Tool]string{SSH: path}}), Host{
		Destination: dest, ControlDir: "/run/fstui-test", ConnectTimeout: time.Second, AliveInterval: time.Second, Persist: time.Second,
	})
	if dest != "down" {
		if _, err := r.Probe(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestRemoteRead(t *testing.T) {
	r := fakeRemote(t, "pve1")
	r.host.Paths[ZFS] = "/usr/bin/printf" // a tool that shows its arguments
	res, err := r.Read(context.Background(), Argv{Tool: ZFS, Options: []string{`%s|`}, Operands: []string{"tank/a b", "it's"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Stdout); got != "--|tank/a b|it's|" {
		t.Errorf("stdout %q", got)
	}
}

// A remote command that fails names itself and the host, not ssh's
// command line.
func TestRemoteCommandFails(t *testing.T) {
	r := fakeRemote(t, "pve1")
	r.host.Paths[ZFS] = "/usr/bin/printf"
	_, err := r.Read(context.Background(), Argv{Tool: ZFS}) // printf without a format fails
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Host != "pve1" || ce.Argv.Tool != ZFS || errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "zfs on pve1 exited with status") || strings.Contains(msg, "BatchMode") {
		t.Errorf("message %q", msg)
	}
}

func TestRemoteUnreachable(t *testing.T) {
	r := fakeRemote(t, "down")
	_, err := r.Probe(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("probe err = %v", err)
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "down could not be reached: ssh: connect to host down") {
		t.Errorf("message %q", msg)
	}
}

// Nothing runs on a host before the probe found its tools.
func TestRemoteBeforeProbe(t *testing.T) {
	r := NewRemote(NewRunner(&Tools{paths: map[Tool]string{SSH: "/bin/false"}}), Host{Destination: "pve1", ControlDir: "/run/x"})
	if _, err := r.Read(context.Background(), Argv{Tool: ZFS}); !errors.Is(err, ErrRemoteTool) {
		t.Errorf("err = %v", err)
	}
}

func TestRemoteStreamAndReadTo(t *testing.T) {
	r := fakeRemote(t, "pve1")
	r.host.Paths[ZFS] = "/usr/bin/printf"
	var lines []string
	err := r.Stream(context.Background(), Argv{Tool: ZFS, Options: []string{`%s\n`, "one", "two"}}, func(b []byte) { lines = append(lines, string(b)) })
	if err != nil || strings.Join(lines, ",") != "one,two" {
		t.Errorf("Stream: %q %v", lines, err)
	}
	var buf bytes.Buffer
	err = r.ReadTo(context.Background(), Argv{Tool: ZFS, Options: []string{"%s", "x"}}, func(rd io.Reader) error {
		_, err := io.Copy(&buf, rd)
		return err
	})
	if err != nil || buf.String() != "x" {
		t.Errorf("ReadTo: %q %v", buf.String(), err)
	}
}

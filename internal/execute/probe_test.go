// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// The probe script run by this machine's own shell finds what Resolve
// finds, and the user running the test.
func TestProbeLocal(t *testing.T) {
	out, err := exec.Command("/bin/sh", "-c", probeScript).Output()
	if err != nil {
		t.Skipf("no POSIX shell with ls and id here: %v", err)
	}
	p, err := ParseProbe(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if p.UID != os.Getuid() {
		t.Errorf("uid %d, want %d", p.UID, os.Getuid())
	}
	for _, tool := range probeTools {
		local, err := Resolve(tool)
		if err != nil {
			continue
		}
		want, _ := local.Path(tool)
		if got := p.Paths[tool]; got != want {
			t.Errorf("%s: probe %q, Resolve %q", tool, got, want)
		}
	}
}

// ls -ldn lines from a Proxmox VE 9.2 node (2026-11-22): merged /usr,
// /sbin a link. The first eight were recorded there; that recording did
// not list /usr/bin, so its two lines are added in the same form.
var pveLs = []string{
	"drwxr-xr-x 19 0 0     23 May 24 11:52 /",
	"lrwxrwxrwx  1 0 0      8 May  8  2026 /sbin -> usr/sbin",
	"-rwxr-xr-x  1 0 0 151760 Aug 22 06:55 /sbin/zfs",
	"-rwxr-xr-x  1 0 0 290288 Aug 22 06:55 /sbin/zpool",
	"drwxr-xr-x 12 0 0     12 May 21  2026 /usr",
	"drwxr-xr-x  2 0 0    470 Nov 22 13:30 /usr/sbin",
	"-rwxr-xr-x  1 0 0 151760 Aug 22 06:55 /usr/sbin/zfs",
	"-rwxr-xr-x  1 0 0 290288 Aug 22 06:55 /usr/sbin/zpool",
	"drwxr-xr-x  2 0 0   1210 Nov 22 13:30 /usr/bin",
	"-rwxr-xr-x  1 0 0  48448 Apr  5  2026 /usr/bin/env",
}

// output is what the script prints, around ls lines and a uid.
func output(uid string, ls []string, extra ...string) []byte {
	parts := append([]string{}, extra...)
	parts = append(parts, markQuote, quoteTest, markQuoteEnd, markUID, uid, markLs)
	parts = append(parts, ls...)
	return []byte(strings.Join(append(parts, markEnd), "\n") + "\n")
}

func TestProbeProxmox(t *testing.T) {
	p, err := ParseProbe(output("0", pveLs, "Linux pve-n1 6.17 #1 SMP", "Welcome"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[Tool]string{RemoteEnv: "/usr/bin/env", ZFS: "/usr/sbin/zfs", Zpool: "/usr/sbin/zpool"}
	if p.UID != 0 || fmt.Sprint(p.Paths) != fmt.Sprint(want) {
		t.Errorf("uid %d paths %v, want 0 %v", p.UID, p.Paths, want)
	}
	if err := p.Require(RemoteEnv, ZFS, Zpool); err != nil {
		t.Error(err)
	}
}

// replace returns pveLs with the line for name changed.
func replace(name, line string) []string {
	out := []string{}
	for _, l := range pveLs {
		if strings.HasSuffix(l, " "+name) {
			l = line
		}
		out = append(out, l)
	}
	return out
}

// Each way a tool or a directory above it could be changed by someone
// other than root leaves the tool out.
func TestProbeUntrusted(t *testing.T) {
	for name, c := range map[string]struct {
		ls   []string
		want string // zfs's path, empty when refused
	}{
		"not root's":          {replace("/usr/sbin/zfs", "-rwxr-xr-x 1 1000 0 1 Aug 22 06:55 /usr/sbin/zfs"), ""},
		"group writable":      {replace("/usr/sbin/zfs", "-rwxrwxr-x 1 0 0 1 Aug 22 06:55 /usr/sbin/zfs"), ""},
		"ACL":                 {replace("/usr/sbin/zfs", "-rwxr-xr-x+ 1 0 0 1 Aug 22 06:55 /usr/sbin/zfs"), ""},
		"SELinux context":     {replace("/usr/sbin/zfs", "-rwxr-xr-x. 1 0 0 1 Aug 22 06:55 /usr/sbin/zfs"), "/usr/sbin/zfs"},
		"a link":              {replace("/usr/sbin/zfs", "lrwxrwxrwx 1 0 0 1 Aug 22 06:55 /usr/sbin/zfs -> /tmp/zfs"), ""},
		"not executable":      {replace("/usr/sbin/zfs", "-rw-r--r-- 1 0 0 1 Aug 22 06:55 /usr/sbin/zfs"), ""},
		"directory writable":  {replace("/usr/sbin", "drwxrwxrwx 2 0 0 1 Nov 22 13:30 /usr/sbin"), ""},
		"parent not root's":   {replace("/usr", "drwxr-xr-x 2 1000 0 1 Nov 22 13:30 /usr"), ""},
		"root dir writable":   {replace("/", "drwxrwxrwt 19 0 0 1 May 24 11:52 /"), ""},
		"next directory used": {append(replace("/usr/sbin/zfs", "-rwxr-xr-x 1 1000 0 1 Aug 22 06:55 /usr/sbin/zfs"), "-rwxr-xr-x 1 0 0 1 Aug 22 06:55 /usr/bin/zfs"), "/usr/bin/zfs"},
	} {
		p, err := ParseProbe(output("0", c.ls))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := p.Paths[ZFS]; got != c.want {
			t.Errorf("%s: zfs = %q, want %q", name, got, c.want)
		}
	}
	p, _ := ParseProbe(output("0", replace("/usr/sbin/zfs", "-rwxr-xr-x 1 1000 0 1 Aug 22 06:55 /usr/sbin/zfs")))
	if err := p.Require(RemoteEnv, ZFS, Zpool); !errors.Is(err, ErrToolMissing) || !strings.Contains(err.Error(), "zfs") {
		t.Errorf("Require = %v", err)
	}
}

func TestProbeOutput(t *testing.T) {
	changed := strings.Replace(string(output("0", pveLs)), quoteTest, strings.ReplaceAll(quoteTest, "\\", ""), 1)
	for name, c := range map[string]struct {
		out  []byte
		want error
	}{
		"quotes changed": {[]byte(changed), ErrShell},
		"stopped early":  {[]byte(strings.Replace(string(output("0", pveLs)), markEnd, "", 1)), ErrProbeOutput},
		"no uid":         {output("root", pveLs), ErrProbeOutput},
		"strange ls":     {output("0", []string{"total 0"}), ErrProbeOutput},
		"nothing":        {nil, ErrProbeOutput},
	} {
		if _, err := ParseProbe(c.out); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

// Messages recorded from OpenSSH 10.0p2 (Debian 13) against this VM's
// sshd, 2026-11-22, each with ssh's exit status 255.
var sshRecorded = []string{
	"ssh: connect to host 127.0.0.1 port 1: Connection refused\n",
	"No ED25519 host key is known for 127.0.0.1 and you have requested strict checking.\nHost key verification failed.\n",
	"Load key \"/dev/null\": error in libcrypto\ndev@127.0.0.1: Permission denied (publickey,password).\n",
	"ssh: Could not resolve hostname no-such-host.invalid: Name or service not known\n",
}

func TestUnreachable(t *testing.T) {
	ssh := Argv{Tool: SSH}
	for _, msg := range append(sshRecorded, "Connection to 10.0.0.1 port 22 timed out\n") {
		if err := error(&CommandError{Argv: ssh, ExitCode: 255, Stderr: termtext.New(msg)}); !errors.Is(err, ErrUnreachable) {
			t.Errorf("not unreachable: %q", msg)
		}
	}
	viaRemote := &CommandError{Argv: Argv{Tool: ZFS}, Host: "pve1", ExitCode: 255, Stderr: termtext.New(sshRecorded[0])}
	if !errors.Is(error(viaRemote), ErrUnreachable) {
		t.Error("a command run through Remote is not unreachable")
	}
	for name, e := range map[string]*CommandError{
		// zpool events refuses a user who is not root with status 255.
		"zpool's 255":     {Argv: ssh, ExitCode: 255, Stderr: termtext.New("cannot get event: permission denied\n")},
		"other status":    {Argv: ssh, ExitCode: 1, Stderr: termtext.New(sshRecorded[0])},
		"local command":   {Argv: Argv{Tool: ZFS}, ExitCode: 255, Stderr: termtext.New(sshRecorded[0])},
		"no error output": {Argv: ssh, ExitCode: 255},
	} {
		if errors.Is(error(e), ErrUnreachable) {
			t.Errorf("%s: taken for unreachable", name)
		}
	}
}

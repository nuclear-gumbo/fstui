// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Host is another machine, reached through the OpenSSH client. Wrap
// turns a command into one ssh runs there, so a Runner runs remote
// commands exactly as local ones.
type Host struct {
	// Destination is what ssh connects to: an alias from the user's
	// ~/.ssh/config, or [user@]host. CheckDestination accepts it.
	Destination string
	// ControlDir holds the sockets of the shared connections; it must
	// be private to fstui's user (statedir.Private).
	ControlDir string
	// ConnectTimeout bounds connecting and the key exchange;
	// AliveInterval is how often an idle connection is checked (ssh
	// gives up after three unanswered checks, its default); Persist is
	// how long a shared connection stays open once nothing uses it. That
	// is the only way one closes: every fstui of the same user shares it,
	// and "ssh -O exit" would cut off the others' commands.
	ConnectTimeout, AliveInterval, Persist time.Duration
	// Paths are the absolute paths of the tools on the host, as the
	// probe found and checked them. A tool not here is not run.
	Paths map[Tool]string
	// HostKeyAlias and KnownHostsFile reach a Proxmox VE node the way
	// the cluster does (PVE::SSHInfo): by address, with the host key
	// looked up under the node's name in the node's own file. Empty
	// leaves both to the user's ssh configuration.
	HostKeyAlias, KnownHostsFile string
}

// SSH is the OpenSSH client, resolved like every other tool.
const SSH Tool = "ssh"

// RemoteEnv is the program that gives a remote command fstui's fixed
// environment; the probe checks it like zfs and zpool.
const RemoteEnv Tool = "env"

var (
	// ErrDestination is returned for a destination fstui does not pass
	// to ssh.
	ErrDestination = errors.New("is not a host name, an address or an ssh alias")
	// ErrRemoteTool is returned for a tool the probe did not find and
	// check on the host. It wraps ErrNotResolved: to a reader, a tool
	// missing on the host is the same as one missing locally.
	ErrRemoteTool = fmt.Errorf("was not checked on this host: %w", ErrNotResolved)
	// ErrOptionPath is returned for a path ssh would misread as the
	// value of an option: the control directory or a known-hosts file.
	ErrOptionPath = errors.New("cannot be given to ssh as a path")
)

// destinationChars are what a destination may contain: host names,
// IPv4 and IPv6 addresses, a user name before @, and ssh aliases made
// of the same. A shell metacharacter in a host name could reach a
// ProxyCommand that the user's ssh_config builds from %h.
const destinationChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-@:"

// maxDestination is the longest destination accepted: a DNS name is at
// most 253 characters, and a user name and @ come before it.
const maxDestination = 320

// CheckDestination accepts a destination for ssh: only the characters
// of host names, addresses and user names, at most one @, and nothing
// that starts with - and so could be read as an option.
func CheckDestination(d string) error {
	switch {
	case d == "", len(d) > maxDestination, strings.Trim(d, destinationChars) != "",
		strings.HasPrefix(d, "-"), strings.HasPrefix(d, "@"), strings.HasSuffix(d, "@"),
		strings.Count(d, "@") > 1:
		return fmt.Errorf("%q %w", d, ErrDestination)
	}
	return nil
}

// maxControlDir is the longest directory whose sockets fit: a Unix
// socket path holds 107 bytes and a NUL on Linux, and ssh first creates
// DIR/%C.RANDOM, with 40 characters for %C and 16 random ones (mux.c,
// muxserver_listen), so 107-58 remain.
const maxControlDir = 49

// checkControlDir refuses a directory that ssh would read as something
// else in a ControlPath option: % starts a token, ~ a home directory,
// and whitespace or quotes split or quote the value.
func checkControlDir(dir string) error {
	if !filepath.IsAbs(dir) || len(dir) > maxControlDir || strings.ContainsAny(dir, "%~\"' \t\n") {
		return fmt.Errorf("%q %w", dir, ErrOptionPath)
	}
	return nil
}

// Wrap returns the command that runs a on the host: ssh with fstui's
// options, then one string the remote shell splits back into exactly
// a's words, run with the same fixed environment as a local command.
func (h Host) Wrap(a Argv) (Argv, error) {
	if err := a.Check(); err != nil {
		return Argv{}, err
	}
	path, ok := h.Paths[a.Tool]
	if !ok {
		return Argv{}, fmt.Errorf("%s on %s: %w", a.Tool, h.Destination, ErrRemoteTool)
	}
	words := append([]string{path}, a.Args()...)
	w, err := h.remote(words)
	if err != nil {
		return Argv{}, err
	}
	w.Done, w.Input, w.Stdin = a.Done, a.Input, a.Stdin
	return w, nil
}

// remote is ssh running words on the host under env -i.
func (h Host) remote(words []string) (Argv, error) {
	envPath, ok := h.Paths[RemoteEnv]
	if !ok {
		return Argv{}, fmt.Errorf("%s on %s: %w", RemoteEnv, h.Destination, ErrRemoteTool)
	}
	quoted := make([]string, 0, 2+len(env)+len(words))
	quoted = append(quoted, shellQuote(envPath), "-i")
	for _, w := range append(slices.Clone(env), words...) {
		quoted = append(quoted, shellQuote(w))
	}
	opts, err := h.options()
	if err != nil {
		return Argv{}, err
	}
	return Argv{Tool: SSH, Options: opts, Operands: []string{h.Destination, strings.Join(quoted, " ")}}, nil
}

// options are ssh's options for every command. Given with -o, they win
// over the user's ~/.ssh/config (ssh_config(5): the command line comes
// first, and the first value is used); everything else, such as User,
// Port, IdentityFile and ProxyJump, comes from it.
func (h Host) options() ([]string, error) {
	if err := CheckDestination(h.Destination); err != nil {
		return nil, err
	}
	if err := checkControlDir(h.ControlDir); err != nil {
		return nil, err
	}
	extra, err := h.keyOptions()
	if err != nil {
		return nil, err
	}
	return append([]string{
		"-T", "-e", "none",
		// No password, passphrase or host key question can appear
		// inside the interface, and an unknown or changed host key is
		// refused, never added.
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=" + seconds(h.ConnectTimeout),
		"-o", "ServerAliveInterval=" + seconds(h.AliveInterval),
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(h.ControlDir, "%C"),
		"-o", "ControlPersist=" + seconds(h.Persist),
	}, extra...), nil
}

// keyOptions are HostKeyAlias and the known-hosts file, checked like the
// destination and the control directory: ssh reads both as config values.
func (h Host) keyOptions() ([]string, error) {
	var out []string
	if h.HostKeyAlias != "" {
		if err := CheckDestination(h.HostKeyAlias); err != nil || strings.Contains(h.HostKeyAlias, "@") {
			return nil, fmt.Errorf("host key alias %q %w", h.HostKeyAlias, ErrDestination)
		}
		out = append(out, "-o", "HostKeyAlias="+h.HostKeyAlias)
	}
	if h.KnownHostsFile != "" {
		if !filepath.IsAbs(h.KnownHostsFile) || strings.ContainsAny(h.KnownHostsFile, "%~\"' \t\n") {
			return nil, fmt.Errorf("known hosts file %q %w", h.KnownHostsFile, ErrOptionPath)
		}
		out = append(out, "-o", "UserKnownHostsFile="+h.KnownHostsFile, "-o", "GlobalKnownHostsFile=none")
	}
	return out, nil
}

// seconds writes d as whole seconds, at least one: ssh reads 0 as "no
// timeout" or "forever".
func seconds(d time.Duration) string {
	return strconv.Itoa(max(int(d/time.Second), 1))
}

// envFor is the environment of a command: the fixed one, and for ssh
// also what it needs to find the user's keys, known hosts and agent.
func envFor(t Tool) []string {
	if t != SSH {
		return env
	}
	return append(slices.Clip(env), sshEnv()...)
}

var sshEnv = sync.OnceValue(func() []string {
	return sshEnvironment(os.Getuid(), os.Getenv("SSH_AUTH_SOCK"), func(uid int) (string, error) {
		u, err := user.LookupId(strconv.Itoa(uid))
		if err != nil {
			return "", err //nolint:wrapcheck // only whether there is one matters
		}
		return u.HomeDir, nil
	})
})

// sshEnvironment is HOME from the password database for uid, not from
// the environment, and SSH_AUTH_SOCK only when it is a socket that uid
// owns: a root fstui started with sudo does not use the invoking
// user's agent.
func sshEnvironment(uid int, agent string, home func(int) (string, error)) []string {
	var out []string
	if h, err := home(uid); err == nil && filepath.IsAbs(h) {
		out = append(out, "HOME="+h)
	}
	if agent == "" || !filepath.IsAbs(agent) {
		return out
	}
	fi, err := os.Lstat(agent) //nolint:gosec // only its type and owner are read; nothing is opened
	if err != nil || fi.Mode().Type() != os.ModeSocket {
		return out
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid {
		out = append(out, "SSH_AUTH_SOCK="+agent)
	}
	return out
}

// sshFailure is the exit status of ssh when ssh itself failed (ssh(1),
// EXIT STATUS).
const sshFailure = 255

// sshMessages are how the OpenSSH client reports that it could not run
// the command, from its source (openssh-portable fa4b2a8): connecting
// (sshconnect.c), name lookup (ssh.c), host keys (sshconnect.c),
// authentication (sshconnect2.c), the connection ending (packet.c,
// clientloop.c) and the shared connection (mux.c). Case matters:
// "permission denied" from zpool is not "Permission denied (".
var sshMessages = []string{
	"ssh: connect to host ",
	": Could not resolve hostname ",
	": Could not resolve host \"",
	"Host key verification failed.",
	" host key is known for ",
	"REMOTE HOST IDENTIFICATION HAS CHANGED",
	": Permission denied (",
	"kex_exchange_identification: ",
	"Connection closed by ",
	"Connection reset by ",
	" closed by remote host.",
	"Timeout, server ",
	"Control socket connect(",
}

// sshFailed reports whether stderr holds one of ssh's own failures, or
// a connection that timed out ("Connection to HOST port 22 timed out").
func sshFailed(stderr string) bool {
	for l := range strings.Lines(stderr) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Connection ") && strings.HasSuffix(l, " timed out") {
			return true
		}
		for _, m := range sshMessages {
			if strings.Contains(l, m) {
				return true
			}
		}
	}
	return false
}

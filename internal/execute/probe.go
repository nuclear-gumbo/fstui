// SPDX-License-Identifier: GPL-3.0-or-later

package execute

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Probe is what the first command on a connection found out about the
// host: who fstui is there, and where the tools are that pass the same
// ownership rule as locally.
type Probe struct {
	UID   int
	Paths map[Tool]string
}

var (
	// ErrShell is returned when the remote login shell gave the test
	// string back changed: it does not quote as a POSIX shell, so the
	// commands fstui builds would not be the ones it checked.
	ErrShell = errors.New("the login shell on the host does not quote as a POSIX shell (sh, bash, dash, zsh)")
	// ErrProbeOutput is returned for output the probe script cannot
	// have printed.
	ErrProbeOutput = errors.New("unexpected output from the host check")
	// ErrUnreachable marks a command that ssh could not run on the
	// host: errors.Is(err, ErrUnreachable) on its CommandError.
	ErrUnreachable = errors.New("the host could not be reached")
)

// probeTools are what the probe looks for, in the system directories
// searched locally. Only env, zfs and zpool are required (Require);
// without systemctl a host has no systemd, as locally.
var probeTools = []Tool{RemoteEnv, ZFS, Zpool, Cat, Ls, Systemctl}

// quoteTest holds every character a shell treats specially in a word,
// and a newline; shellQuote quotes it, and the shell must print it back.
const quoteTest = "a b'c\"$x\\\n;y`z`&*~"

const (
	markQuote, markQuoteEnd  = "fstui-probe-quote", "fstui-probe-quote-end"
	markUID, markLs, markEnd = "fstui-probe-uid", "fstui-probe-ls", "fstui-probe-end"
)

// probeScript runs in the remote login shell. It uses only printf, id
// and ls, which every POSIX system has, and prints markers so that a
// banner or a message from the shell's start files cannot be taken for
// an answer.
var probeScript = func() string {
	var names []string
	for _, d := range systemDirs {
		for p := d; ; p = path.Dir(p) {
			if !slices.Contains(names, p) {
				names = append(names, p)
			}
			if p == "/" {
				break
			}
		}
		for _, t := range probeTools {
			names = append(names, path.Join(d, string(t)))
		}
	}
	return strings.Join([]string{
		"printf '" + markQuote + `\n%s\n` + markQuoteEnd + `\n' ` + shellQuote(quoteTest),
		"printf '" + markUID + `\n'`, "id -u",
		"printf '" + markLs + `\n'`, "LC_ALL=C ls -ldn " + strings.Join(names, " ") + " 2>/dev/null",
		"printf '" + markEnd + `\n'`,
	}, "; ")
}()

// ProbeArgv is the probe for the host: the only command fstui sends
// that is not one tool's argv, and a constant.
func (h Host) ProbeArgv() (Argv, error) {
	opts, err := h.options()
	if err != nil {
		return Argv{}, err
	}
	return Argv{Tool: SSH, Options: opts, Operands: []string{h.Destination, probeScript}}, nil
}

// ParseProbe reads the probe's output. It fails when the shell changed
// the test string or the output is not the script's; a tool that is
// missing or untrusted is simply absent from Paths (see Require).
func ParseProbe(out []byte) (Probe, error) {
	sections, err := probeSections(out)
	if err != nil {
		return Probe{}, err
	}
	if _, ok := sections[markEnd]; !ok {
		return Probe{}, fmt.Errorf("%w: it stopped before the end", ErrProbeOutput)
	}
	if strings.Join(sections[markQuote], "\n") != quoteTest {
		return Probe{}, ErrShell
	}
	uids := sections[markUID]
	uid, err := strconv.Atoi(strings.Join(uids, ""))
	if len(uids) != 1 || err != nil || uid < 0 {
		return Probe{}, fmt.Errorf("%w: user id %q", ErrProbeOutput, uids)
	}
	entries := map[string]lsEntry{}
	for _, l := range sections[markLs] {
		e, name, ok := parseLs(l)
		if !ok {
			return Probe{}, fmt.Errorf("%w: %q", ErrProbeOutput, l)
		}
		entries[name] = e
	}
	return Probe{UID: uid, Paths: toolPaths(entries)}, nil
}

// probeSections splits the output at the markers; lines before the
// first, such as a banner, belong to none.
func probeSections(out []byte) (map[string][]string, error) {
	sections := map[string][]string{}
	var cur string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		switch line := sc.Text(); line {
		case markQuote, markQuoteEnd, markUID, markLs, markEnd:
			cur = line
			sections[cur] = []string{}
		default:
			if cur != "" {
				sections[cur] = append(sections[cur], line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProbeOutput, err)
	}
	return sections, nil
}

// toolPaths is each tool in the first system directory that holds a
// trusted, executable copy, as Resolve searches locally.
func toolPaths(entries map[string]lsEntry) map[Tool]string {
	paths := map[Tool]string{}
	for _, t := range probeTools {
		for _, d := range systemDirs {
			f := path.Join(d, string(t))
			if e, ok := entries[f]; ok && trustedDirs(entries, d) && e.trusted('-') && e.mode[3] == 'x' {
				paths[t] = f
				break
			}
		}
	}
	return paths
}

// ErrToolMissing is returned by Require for a tool the probe did not
// find in a root-owned system directory on the host.
var ErrToolMissing = errors.New("not found in a system directory that only root can change")

// Require returns an error naming every tool in need that the probe did
// not find and trust.
func (p Probe) Require(need ...Tool) error {
	var missing []string
	for _, t := range need {
		if _, ok := p.Paths[t]; !ok {
			missing = append(missing, string(t))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: %w", strings.Join(missing, ", "), ErrToolMissing)
	}
	return nil
}

// lsEntry is one line of ls -ldn: the mode string and the owner's uid.
type lsEntry struct {
	mode string
	uid  int
}

// parseLs reads "MODE LINKS UID GID SIZE DATE NAME" or, for a link,
// "... NAME -> TARGET". The probe asks only for fixed paths without
// spaces, so the name is one field.
func parseLs(line string) (lsEntry, string, bool) {
	f := strings.Fields(line)
	if i := slices.Index(f, "->"); i > 0 {
		f = f[:i]
	}
	const minFields = 7 // mode, links, uid, gid, size, a date of at least one field, name
	if len(f) < minFields || len(f[0]) < len("drwxr-xr-x") {
		return lsEntry{}, "", false
	}
	uid, err := strconv.Atoi(f[2])
	if err != nil {
		return lsEntry{}, "", false
	}
	return lsEntry{mode: f[0], uid: uid}, f[len(f)-1], true
}

// trusted is the local rule (resolver.checkFile, checkDirs) applied to
// an ls line: the expected type, owned by root, writable by no group or
// other user, and no ACL that could say otherwise ("+" after the mode;
// "." marks only an SELinux context).
func (e lsEntry) trusted(kind byte) bool {
	const modeLen = 10
	extra := e.mode[modeLen:]
	return e.mode[0] == kind && e.uid == rootUID && e.mode[5] != 'w' && e.mode[8] != 'w' && (extra == "" || extra == ".")
}

// trustedDirs checks dir and every directory above it. A directory that
// is a symbolic link, such as /sbin on a merged-/usr system, is not a
// candidate: its target is searched in its own place in systemDirs.
func trustedDirs(entries map[string]lsEntry, dir string) bool {
	for d := dir; ; d = path.Dir(d) {
		if e, ok := entries[d]; !ok || !e.trusted('d') {
			return false
		}
		if d == "/" {
			return true
		}
	}
}

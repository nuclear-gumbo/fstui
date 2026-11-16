// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

// Package demo is fstui's demo backend: it answers the zfs and zpool
// commands fstui runs from output recorded on OpenZFS 2.3.9, and makes up
// I/O and ARC activity around it, so fstui can be tried in a container or
// anywhere without ZFS. It never starts a process. Only the demo build
// (tag fstui_demo) compiles it.
package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nuclear-gumbo/fstui"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// pools maps each recorded scenario to the name its pool has in the demo.
// The recordings use fstuitest_ names on sparse files; the demo shows
// names and devices as they might look on a real machine.
var pools = []struct{ dir, recorded, name string }{
	{"healthy", "fstuitest_g_healthy", "tank"},
	{"degraded", "fstuitest_g_degraded", "backup"},
	{"resilver-in-progress", "fstuitest_g_resilver", "archive"},
	{"scrub-in-progress", "fstuitest_g_scrub", "media"},
	{"roles", "fstuitest_g_roles", "fast"},
	{"checkpoint", "fstuitest_g_ckpt", "vault"},
	{"checksum-repaired", "fstuitest_g_cksum", "scratch"},
}

// devicePrefixes are the file names some recordings used for their
// devices instead of the pool name.
var devicePrefixes = map[string]string{
	"fstuitest_g_ck": "scratch", "fstuitest_g_deg": "backup", "fstuitest_g_rs": "archive", "fstuitest_g_sc": "media",
	"fstuitest_ev": "tank",
}

// eventsPool is the pool of the recorded events (faults induced on a
// test mirror), shown as tank.
const eventsPool = "fstuitest_ev"

var (
	mountRE  = regexp.MustCompile(`/var/tmp/fstui-test/mnt/`)
	deviceRE = regexp.MustCompile(`/var/tmp/fstui-test/(fstuitest_[a-z_]+?)-([a-z0-9]+)\.img`)
)

// rename turns recorded names into the demo's.
func rename(b []byte) []byte {
	b = mountRE.ReplaceAll(b, []byte("/"))
	b = deviceRE.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := deviceRE.FindSubmatch(m)
		return fmt.Appendf(nil, "/dev/disk/by-id/ata-DEMO_%s_%s", strings.ToUpper(demoName(string(sub[1]))), strings.ToUpper(string(sub[2])))
	})
	for _, p := range pools {
		b = bytes.ReplaceAll(b, []byte(p.recorded), []byte(p.name))
	}
	return bytes.ReplaceAll(b, []byte(eventsPool), []byte("tank"))
}

func demoName(recorded string) string {
	if n, ok := devicePrefixes[recorded]; ok {
		return n
	}
	for _, p := range pools {
		if p.recorded == recorded {
			return p.name
		}
	}
	return recorded
}

// Commands answers zfs and zpool commands from the recordings. It
// implements zfs.Commands.
type Commands struct {
	mu    sync.Mutex
	cache map[string][]byte
	io    *ioModel
	st    *state // datasets, snapshots and bookmarks, loaded on first use
	// poolSt holds the demo's changes to pools (scrubs, cleared errors).
	poolSt map[string]*poolState
	arc    *ARC     // the model the ARC view reads (UseARC)
	files  arcFiles // ARC limits and the modprobe file
	clock  func() time.Time
}

// New returns the demo's commands.
func New() *Commands { return &Commands{cache: map[string][]byte{}} }

var _ zfs.Commands = (*Commands)(nil)

// file returns a recorded file of a scenario, renamed.
func (c *Commands) file(dir, name string) ([]byte, error) {
	p := path.Join(fstui.RecordedRoot, dir, name)
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.cache[p]; ok {
		return b, nil
	}
	b, err := fs.ReadFile(fstui.Recorded, p)
	if err != nil {
		return nil, fmt.Errorf("demo recording %s: %w", p, err)
	}
	b = rename(b)
	if strings.HasSuffix(name, ".json") && name != "zfs-version.json" {
		if b, err = scaleJSON(b); err != nil {
			return nil, err
		}
	}
	c.cache[p] = b
	return b, nil
}

// errNoRecording is returned for a command the demo cannot answer; the
// view shows it like any failed read.
var errNoRecording = errors.New("the demo has no recording for this command")

// Read implements zfs.Commands.
func (c *Commands) Read(_ context.Context, a execute.Argv) (execute.Result, error) {
	out, err := c.answer(a)
	var partial *partialError
	if errors.As(err, &partial) {
		stderr := termtext.New(partial.stderr)
		return execute.Result{Stdout: partial.out, ExitCode: 1, Stderr: stderr}, &execute.CommandError{Argv: a, ExitCode: 1, Stderr: stderr}
	}
	if err != nil {
		return execute.Result{ExitCode: 1, Stderr: termtext.New(err.Error())},
			&execute.CommandError{Argv: a, ExitCode: 1, Stderr: termtext.New(err.Error())}
	}
	return execute.Result{Stdout: out}, nil
}

func (c *Commands) answer(a execute.Argv) ([]byte, error) {
	if out, ok, err := c.textAnswer(a); ok {
		return out, err
	}
	if out, ok := diskAnswer(a); ok {
		return out, nil
	}
	cmd := string(a.Tool) + " " + strings.Join(a.Options, " ")
	switch {
	case cmd == "zfs version -j":
		return c.file("", "zfs-version.json")
	case cmd == "zfs version":
		return c.file("", "zfs-version.txt")
	case cmd == "zpool list -j --json-int":
		return c.pools("zpool-list.json", a.Operands)
	case cmd == "zpool status -j --json-int", cmd == "zpool status -j --json-int -i -t":
		return c.pools("zpool-status.json", a.Operands)
	case cmd == "zpool get -j -p all":
		return c.pools("zpool-get-p.json", a.Operands)
	case cmd == "zfs allow" && len(a.Operands) == 1:
		return c.allow(a.Operands[0])
	case cmd == "systemctl list-timers --all --output=json --no-pager":
		b, err := fs.ReadFile(fstui.Recorded, fstui.RecordedTimers)
		if err != nil {
			return nil, fmt.Errorf("demo recording: %w", err)
		}
		// The recorded weekly timer of fstuitest_sched becomes tank's.
		return bytes.ReplaceAll(b, []byte("fstuitest_sched"), []byte("tank")), nil
	case cmd == "zpool events -H -v":
		return c.file("events", "induced-v.txt")
	case cmd == "zpool history -i" && len(a.Operands) == 1:
		for _, p := range pools {
			if p.name == a.Operands[0] {
				return c.file(p.dir, "zpool-history.txt")
			}
		}
	case cmd == "zfs get -j -p all" && len(a.Operands) == 1:
		return c.datasets("zfs get", func(name, _ string) bool { return name == a.Operands[0] })
	case strings.HasPrefix(cmd, "zfs list -j --json-int "):
		return c.list(a)
	case cmd == "zfs diff -H -F -t" && len(a.Operands) >= 1:
		return c.diff(a.Operands[0])
	}
	return nil, fmt.Errorf("%w: %s", errNoRecording, a)
}

// pools merges one recorded zpool file across the scenarios, keeping the
// pools named in operands, or all of them.
func (c *Commands) pools(file string, operands []string) ([]byte, error) {
	var head json.RawMessage
	merged := map[string]json.RawMessage{}
	for _, p := range pools {
		if len(operands) > 0 && !slices.Contains(operands, p.name) {
			continue
		}
		b, err := c.file(p.dir, file)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Head  json.RawMessage            `json:"output_version"`
			Pools map[string]json.RawMessage `json:"pools"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("demo recording %s/%s: %w", p.dir, file, err)
		}
		head = doc.Head
		for k, v := range doc.Pools {
			if file == "zpool-status.json" {
				c.mu.Lock()
				v, err = c.patchStatus(k, v)
				c.mu.Unlock()
				if err != nil {
					return nil, err
				}
			}
			merged[k] = v
		}
	}
	if head == nil {
		return nil, fmt.Errorf("%w: no pool %s", errNoRecording, strings.Join(operands, ", "))
	}
	return marshal(map[string]any{"output_version": head, "pools": merged})
}

func marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("demo answer: %w", err)
	}
	return b, nil
}

// datasets answers from the demo's state, loaded from the recorded zfs
// get -p all -r of every pool, which holds every property of every
// dataset and snapshot, keeping the entries keep accepts (by name and
// type). command goes into the JSON envelope, because fstui checks it.
func (c *Commands) datasets(command string, keep func(name, typ string) bool) ([]byte, error) {
	st, err := c.load()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	merged := map[string]*entry{}
	for name, e := range st.entries {
		if keep(name, strings.ToLower(e.Type)) {
			merged[name] = e
		}
	}
	head := map[string]any{"command": command, "vers_major": 0, "vers_minor": 1}
	return marshal(map[string]any{"output_version": head, "datasets": merged})
}

// list answers zfs list -j: -t picks the types, -d 0 the named datasets
// themselves, -d 1 their snapshots, -r everything below them, and no
// operand every pool.
func (c *Commands) list(a execute.Argv) ([]byte, error) {
	var types []string
	depth := ""
	recursive := false
	for i := 0; i < len(a.Options); i++ {
		switch a.Options[i] {
		case "-t":
			i++
			types = strings.Split(a.Options[i], ",")
		case "-d":
			i++
			depth = a.Options[i]
		case "-o":
			i++ // every property is answered; fstui reads the ones it asked for
		case "-r":
			recursive = true
		}
	}
	return c.datasets("zfs list", func(name, typ string) bool {
		if len(types) > 0 && !slices.Contains(types, typ) {
			return false
		}
		if len(a.Operands) == 0 {
			return true
		}
		for _, op := range a.Operands {
			switch {
			case name == op && depth != "1":
				return true
			case depth == "1" && strings.HasPrefix(name, op+"@"):
				return true
			case recursive && (strings.HasPrefix(name, op+"/") || strings.HasPrefix(name, op+"@")):
				return true
			}
		}
		return false
	})
}

// allow answers zfs allow: the delegations recorded on a test pool
// (fstuitest_allow, home and home/sub) are shown on tank/home/user and
// its parents; other datasets have none, as recorded on a pool without
// delegations.
func (c *Commands) allow(ds string) ([]byte, error) {
	if ds != "tank/home/user" {
		return c.file("allow", "none.txt")
	}
	b, err := c.file("allow", "nested.txt")
	if err != nil {
		return nil, err
	}
	b = bytes.ReplaceAll(b, []byte("fstuitest_allow/home/sub"), []byte("tank/home/user"))
	return bytes.ReplaceAll(b, []byte("fstuitest_allow"), []byte("tank")), nil
}

// Version is the recorded OpenZFS version.
func (c *Commands) Version() (zfs.Version, error) {
	b, err := c.file("", "zfs-version.json")
	if err != nil {
		return zfs.Version{}, err
	}
	return zfs.ParseVersion(b)
}

// diff answers zfs diff with a few made-up changes under the snapshot's
// dataset, as the recordings have no file changes to compare.
func (c *Commands) diff(snap string) ([]byte, error) {
	st, err := c.load()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := st.entries[snap]
	if !ok || !strings.Contains(snap, "@") {
		return nil, fmt.Errorf("cannot open '%s': dataset does not exist", snap)
	}
	mp := st.entries[datasetOf(snap)].get("mountpoint")
	if !strings.HasPrefix(mp, "/") {
		mp = "/" + datasetOf(snap)
	}
	t, _ := strconv.ParseInt(e.get("creation"), 10, 64)
	changes := []struct{ change, typ, path, to string }{
		{"M", "/", "", ""},
		{"M", "F", "notes.txt", ""},
		{"+", "F", "report-2026.odt", ""},
		{"-", "F", "old-draft.odt", ""},
		{"R", "F", "photos/IMG_0001.jpg", "photos/beach.jpg"},
		{"+", "/", "photos/trip", ""},
	}
	var b strings.Builder
	for i, ch := range changes {
		fmt.Fprintf(&b, "%d.000000000\t%s\t%s\t%s/%s", t+int64(3600*(i+1)), ch.change, ch.typ, mp, ch.path)
		if ch.to != "" {
			fmt.Fprintf(&b, "\t%s/%s", mp, ch.to)
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

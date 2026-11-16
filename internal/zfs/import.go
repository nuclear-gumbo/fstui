// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Importable is a pool zpool import found and could import: its name,
// numeric id, state, and the explanation and device tree zpool prints.
type Importable struct {
	Name    termtext.Text
	ID      string // decimal; importing by id is unambiguous
	State   string // ONLINE, DEGRADED, UNAVAIL, FAULTED
	Status  []termtext.Text
	Action  []termtext.Text
	Devices []ImportDevice
}

// ImportDevice is one line of an importable pool's device tree.
type ImportDevice struct {
	Depth int // 0 for the pool itself
	Name  termtext.Text
	State string
	Note  termtext.Text // e.g. "cannot open"
}

// ParseImportable parses zpool import without a pool argument (OpenZFS
// 2.3.9 has no JSON for it; testdata/zfs/2.3.9/import/). Each pool is a
// block of "key: value" lines, continued by indented lines, then the
// device tree after "config:".
func ParseImportable(out []byte) ([]Importable, error) {
	var pools []Importable
	var cur *Importable
	var key string
	inConfig := false
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		k, v, isKey := strings.Cut(trimmed, ":")
		isKey = isKey && !strings.HasPrefix(line, "\t") && !strings.Contains(k, " ")
		switch {
		case isKey && k == "pool":
			pools = append(pools, Importable{Name: termtext.New(strings.TrimSpace(v))})
			cur, key, inConfig = &pools[len(pools)-1], k, false
		case cur == nil || trimmed == "":
		case isKey && k == "config":
			inConfig = true
		case inConfig:
			cur.Devices = append(cur.Devices, parseImportDevice(line))
		case isKey:
			key = k
			cur.add(key, strings.TrimSpace(v))
		default: // a continuation of the last key
			cur.add(key, trimmed)
		}
	}
	for _, p := range pools {
		if p.ID == "" || p.Name.IsEmpty() {
			return nil, fmt.Errorf("zpool import: a pool without a name or id: %q", p.Name.Raw())
		}
	}
	return pools, nil
}

func (p *Importable) add(key, v string) {
	switch key {
	case "id":
		p.ID = v
	case "state":
		p.State = v
	case "status":
		p.Status = append(p.Status, termtext.New(v))
	case "action":
		p.Action = append(p.Action, termtext.New(v))
	}
}

// parseImportDevice reads "\t  mirror-0   DEGRADED  note": a tab, then two
// spaces per level.
func parseImportDevice(line string) ImportDevice {
	body := strings.TrimPrefix(line, "\t")
	indent := len(body) - len(strings.TrimLeft(body, " "))
	f := strings.Fields(body)
	d := ImportDevice{Depth: indent / 2}
	if len(f) > 0 {
		d.Name = termtext.New(f[0])
	}
	if len(f) > 1 {
		d.State = f[1]
	}
	if len(f) > 2 {
		d.Note = termtext.New(strings.Join(f[2:], " "))
	}
	return d
}

// Importable lists the pools zpool import finds, in dirs (each passed
// with -d), or where zpool looks by default when dirs is empty. It only
// reads the devices' labels.
func (r *Reader) Importable(ctx context.Context, dirs []string) ([]Importable, error) {
	opts := []string{"import"}
	for _, d := range dirs {
		opts = append(opts, "-d", d)
	}
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: opts})
	if err != nil {
		return nil, err
	}
	return ParseImportable(out)
}

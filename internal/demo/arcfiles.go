// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// The files the ARC limit changes read and write: module parameters,
// arcstats and /etc/modprobe.d/zfs.conf, kept in memory. ReadFile
// replaces the reader's own, so the demo never reads the host's files.

// arcFiles is the demo's copy of those files.
type arcFiles struct {
	max, min uint64 // zfs_arc_max, zfs_arc_min; 0 is automatic
	conf     []byte // nil: no modprobe file
	pending  []byte // written by tee, not yet moved into place
}

// UseARC gives the demo's commands the ARC model the views show, so a
// limit set in the demo shows in the ARC view.
func (c *Commands) UseARC(a *ARC) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.arc = a
}

// ReadFile answers the reader's file reads from the demo's copies, and
// the kstats tune mode reads from its replay.
func (c *Commands) ReadFile(path string) ([]byte, error) {
	if pool, file, ok := c.kstatPool(path); ok && file != "" {
		if b, found, err := c.tuneFile(pool, file); found {
			return b, err
		}
	}
	switch path {
	case zfs.KstatDir + "/dmu_tx", zfs.KstatDir + "/zil", zfs.KstatDir + "/zfetchstats":
		if b, found, err := c.tuneFile("", strings.TrimPrefix(path, zfs.KstatDir+"/")); found {
			return b, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch path {
	case zfs.ParamDir + "/zfs_arc_max":
		return []byte(strconv.FormatUint(c.files.max, 10) + "\n"), nil
	case zfs.ParamDir + "/zfs_arc_min":
		return []byte(strconv.FormatUint(c.files.min, 10) + "\n"), nil
	case zfs.ARCStatsPath:
		if c.arc != nil {
			return c.arc.kstat(), nil
		}
	case zfs.ModprobeConf:
		if c.files.conf != nil {
			return c.files.conf, nil
		}
	}
	return nil, fmt.Errorf("demo: %s: %w", path, fs.ErrNotExist)
}

// applyFile carries out tee, cp, chmod and mv on the demo's files.
func (c *Commands) applyFile(a execute.Argv, stdin []byte) (execute.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ops := a.Operands
	switch {
	case a.Tool == execute.Tee && len(ops) == 1 && strings.HasPrefix(ops[0], zfs.ParamDir+"/"):
		n, err := strconv.ParseUint(strings.TrimSpace(string(stdin)), 10, 64)
		if err != nil {
			return failed(a, fmt.Errorf("write error: %w", err))
		}
		switch strings.TrimPrefix(ops[0], zfs.ParamDir+"/") {
		case "zfs_arc_max":
			c.files.max = n
		case "zfs_arc_min":
			c.files.min = n
		default:
			return failed(a, fmt.Errorf("%w: %s", errUnsupported, a))
		}
		if c.arc != nil {
			c.arc.setLimits(c.files.max, c.files.min)
		}
	case a.Tool == execute.Tee && len(ops) == 1 && ops[0] == zfs.ModprobeConf+".fstui-new":
		c.files.pending = append([]byte(nil), stdin...)
	case a.Tool == execute.Chmod && len(ops) == 1 && ops[0] == zfs.ModprobeConf+".fstui-new":
		// the demo keeps no modes
	case a.Tool == execute.Mv && len(ops) == 2 && ops[1] == zfs.ModprobeConf:
		c.files.conf, c.files.pending = c.files.pending, nil
	case a.Tool == execute.Cp && len(ops) == 2 && ops[0] == zfs.ModprobeConf:
		// the backup: nothing to show in the demo
	default:
		return failed(a, fmt.Errorf("%w: %s", errUnsupported, a))
	}
	return execute.Result{Duration: time.Millisecond}, nil
}

// setLimits applies new limits to the model: a lower maximum caps what
// the ARC can grow to; it then drifts down to it as the views read.
func (a *ARC) setLimits(maxB, minB uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if maxB != 0 {
		a.s.Max = maxB
		a.s.Target = min(a.s.Target, maxB)
	}
	if minB != 0 {
		a.s.Min = minB
	}
}

// kstat writes the model as /proc/spl/kstat/zfs/arcstats would.
func (a *ARC) kstat() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	var b strings.Builder
	b.WriteString("13 1 0x01 147 39984 0 0\nname                            type data\n")
	for _, f := range []struct {
		name string
		v    uint64
	}{
		{"hits", a.s.Hits},
		{"misses", a.s.Misses},
		{"c", a.s.Target},
		{"c_min", a.s.Min},
		{"c_max", a.s.Max},
		{"size", a.s.Size},
		{"memory_all_bytes", a.s.Memory},
	} {
		fmt.Fprintf(&b, "%-31s 4    %d\n", f.name, f.v)
	}
	return []byte(b.String())
}

// ReadDir answers the reader's directory listings: a pool's kstat
// directory, from the replayed recordings (tunefiles.go). The host's
// /proc is never listed.
func (c *Commands) ReadDir(p string) ([]string, error) {
	if pool, file, ok := c.kstatPool(p); ok && file == "" {
		return c.tuneDir(pool)
	}
	return nil, fmt.Errorf("demo: %s: %w", p, fs.ErrNotExist)
}

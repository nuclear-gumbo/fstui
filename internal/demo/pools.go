// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Pool changes in the demo: a scrub started, paused, resumed or stopped
// there shows in the pools' status, and clearing errors sets the
// counts to zero. Trim and initialize are accepted; the recordings do
// not include their progress.

// poolState is what the demo changed about a pool's status.
type poolState struct {
	scan    map[string]any // replaces scan_stats; nil keeps the recording
	cleared bool
	offline map[string]bool // devices taken offline (true) or brought back (false)
	clear   map[string]bool // devices whose errors were cleared
}

// applyPool carries out a zpool change on the demo's pools.
func (c *Commands) applyPool(a execute.Argv) (execute.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(a.Options) == 0 || len(a.Operands) == 0 || len(a.Operands) > 2 {
		return failed(a, fmt.Errorf("%w: %s", errUnsupported, a))
	}
	pool := a.Operands[0]
	if c.poolSt == nil {
		c.poolSt = map[string]*poolState{}
	}
	ps := c.poolSt[pool]
	if ps == nil {
		ps = &poolState{}
		c.poolSt[pool] = ps
	}
	now := c.now().Unix()
	if len(a.Operands) == 2 {
		return c.applyDevice(a, ps)
	}
	switch strings.Join(a.Options, " ") {
	case "scrub":
		if ps.scan != nil && ps.scan["scrub_pause"] != int64(0) && ps.scan["state"] == "SCANNING" {
			ps.scan["scrub_pause"] = int64(0) // resume
		} else {
			ps.scan = map[string]any{
				"function": "SCRUB", "state": "SCANNING", "start_time": now, "end_time": int64(0),
				"to_examine": int64(1 << 40), "examined": int64(0), "issued": int64(0), "processed": int64(0), "errors": int64(0), "scrub_pause": int64(0),
			}
		}
	case "scrub -p":
		if ps.scan != nil {
			ps.scan["scrub_pause"] = now
		}
	case "scrub -s":
		if ps.scan != nil {
			ps.scan["state"], ps.scan["end_time"] = "CANCELED", now
		}
	case "scrub -e":
		ps.scan = map[string]any{
			"function": "ERRORSCRUB", "state": "FINISHED", "start_time": now, "end_time": now,
			"to_examine": int64(0), "examined": int64(0), "issued": int64(0), "processed": int64(0), "errors": int64(0), "scrub_pause": int64(0),
		}
	case "clear":
		ps.cleared = true
	case "trim", "initialize":
	default:
		return failed(a, fmt.Errorf("%w: %s", errUnsupported, a))
	}
	return execute.Result{Duration: 3 * time.Millisecond}, nil
}

// applyDevice carries out online, offline and clear of one device.
func (c *Commands) applyDevice(a execute.Argv, ps *poolState) (execute.Result, error) {
	dev := a.Operands[1]
	if ps.offline == nil {
		ps.offline, ps.clear = map[string]bool{}, map[string]bool{}
	}
	switch strings.Join(a.Options, " ") {
	case "offline":
		ps.offline[dev] = true
	case "online":
		ps.offline[dev] = false
	case "clear":
		ps.clear[dev] = true
	default:
		return failed(a, fmt.Errorf("%w: %s", errUnsupported, a))
	}
	return execute.Result{Duration: 3 * time.Millisecond}, nil
}

func failed(a execute.Argv, err error) (execute.Result, error) {
	msg := termtext.New(err.Error() + "\n")
	return execute.Result{ExitCode: 1, Stderr: msg}, &execute.CommandError{Argv: a, ExitCode: 1, Stderr: msg}
}

// patchStatus applies the demo's pool changes to one pool's recorded
// zpool status entry. Called with c.mu held.
func (c *Commands) patchStatus(pool string, raw json.RawMessage) (json.RawMessage, error) {
	ps := c.poolSt[pool]
	if ps == nil {
		return raw, nil
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("demo status of %s: %w", pool, err)
	}
	if ps.scan != nil {
		p["scan_stats"] = ps.scan
	}
	if ps.cleared {
		p["error_count"] = 0
		zeroErrors(p)
	}
	// Each read patches the recording afresh, so only what is down now
	// needs marking; a device brought back shows as recorded.
	if (len(ps.offline) > 0 || len(ps.clear) > 0) && patchDevices(p, ps) {
		p["state"] = "DEGRADED"
	}
	out, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("demo status of %s: %w", pool, err)
	}
	return out, nil
}

// zeroErrors sets every device's error counts below v to zero.
func zeroErrors(v map[string]any) {
	for _, k := range []string{"read_errors", "write_errors", "checksum_errors"} {
		if _, ok := v[k]; ok {
			v[k] = 0
		}
	}
	for _, group := range []string{"vdevs", "special", "dedup", "logs", "l2cache", "spares"} {
		children, _ := v[group].(map[string]any)
		for _, ch := range children {
			if m, ok := ch.(map[string]any); ok {
				zeroErrors(m)
			}
		}
	}
}

// patchDevices applies device changes below v and reports whether a
// device below it is offline, marking its vdevs DEGRADED.
func patchDevices(v map[string]any, ps *poolState) bool {
	down := false
	name, _ := v["name"].(string)
	if off, ok := ps.offline[name]; ok {
		v["state"] = map[bool]string{true: "OFFLINE", false: "ONLINE"}[off]
		down = off
	}
	if ps.clear[name] {
		for _, k := range []string{"read_errors", "write_errors", "checksum_errors"} {
			v[k] = 0
		}
	}
	children := false
	for _, group := range []string{"vdevs", "special", "dedup", "logs", "l2cache", "spares"} {
		kids, _ := v[group].(map[string]any)
		for _, ch := range kids {
			if m, ok := ch.(map[string]any); ok {
				children = true
				if patchDevices(m, ps) {
					down = true
				}
			}
		}
	}
	if children && down && v["vdev_type"] != "root" {
		v["state"] = "DEGRADED"
	}
	return down
}

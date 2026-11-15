// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// ARC limits (zfs(4), 2.3.9): zfs_arc_max must be at least 64 MiB and
// cannot be set back to 0 (automatic) while the module is loaded;
// lowering it does not shrink the ARC until memory is needed.
const (
	minARCMax = 64 << 20
	minARCMin = 32 << 20
)

// arcWish is what the user asked for: nil leaves a limit as it is.
type arcWish struct{ max, min *uint64 }

func parseWish(maxS, minS string) (arcWish, error) {
	var w arcWish
	for _, f := range []struct {
		s   string
		dst **uint64
	}{{maxS, &w.max}, {minS, &w.min}} {
		if f.s == "" {
			continue
		}
		n, err := ParseSize(f.s)
		if err != nil {
			return arcWish{}, err
		}
		*f.dst = &n
	}
	if w.max == nil && w.min == nil {
		return arcWish{}, fmt.Errorf("give a maximum, a minimum or both")
	}
	return w, nil
}

// check blocks limits the module would ignore or refuse.
func (w arcWish) check(p *mutate.Plan, l zfs.ARCLimits, running bool) {
	effMax := l.Stats.Max
	if w.max != nil && *w.max != 0 {
		effMax = *w.max
	}
	if w.max != nil {
		m := *w.max
		blockIf(p, m == 0 && running && l.SetMax != 0, "the maximum cannot go back to automatic (0) while the module is loaded; keep 0 for the next boot instead")
		blockIf(p, m != 0 && m < minARCMax, "the maximum must be at least 64 MiB; zfs ignores less")
		blockIf(p, m != 0 && l.Stats.Memory > 0 && m >= l.Stats.Memory, "the maximum must be below the system's memory ("+iec(l.Stats.Memory)+")")
	}
	if w.min != nil && *w.min != 0 {
		blockIf(p, *w.min < minARCMin, "the minimum must be at least 32 MiB")
		blockIf(p, *w.min >= effMax, "the minimum must be below the maximum ("+iec(effMax)+")")
	}
}

func paramArgv(name string, v uint64) execute.Argv {
	return execute.Argv{Tool: execute.Tee, Operands: []string{zfs.ParamDir + "/" + name}, Input: []byte(strconv.FormatUint(v, 10) + "\n")}
}

func limitText(v uint64) string {
	if v == 0 {
		return "automatic"
	}
	return iec(v)
}

// ARCSet plans setting zfs_arc_max and zfs_arc_min now, until the next
// reboot or module reload.
func (b Builder) ARCSet(_ context.Context, maxS, minS string) (*mutate.Plan, error) {
	w, err := parseWish(maxS, minS)
	if err != nil {
		return nil, err
	}
	l, err := b.R.ARCLimits()
	if err != nil {
		return nil, err
	}
	p := newPlan(mutate.ARCSet, "Set the ARC limits", []string{"arc"}, map[string]string{"max": maxS, "min": minS})
	blockIf(p, b.Unprivileged, "ARC limits need root")
	w.check(p, l, true)
	var steps []execute.Argv
	if w.max != nil {
		steps = append(steps, paramArgv("zfs_arc_max", *w.max))
		affects(p, "maximum: "+limitText(l.SetMax)+" → "+limitText(*w.max))
		if *w.max != 0 && *w.max < l.Stats.Size {
			affects(p, "the ARC holds "+iec(l.Stats.Size)+" now and shrinks to the new maximum only when memory is needed")
		}
	}
	if w.min != nil {
		minStep := paramArgv("zfs_arc_min", *w.min)
		// The minimum must stay below the maximum at every step: set it
		// first when the maximum comes down below the current minimum.
		if w.max != nil && *w.max != 0 && *w.max <= l.Stats.Min {
			steps = append([]execute.Argv{minStep}, steps...)
		} else {
			steps = append(steps, minStep)
		}
		affects(p, "minimum: "+limitText(l.SetMin)+" → "+limitText(*w.min))
	}
	p.Steps = steps
	affects(p, "this lasts until the next reboot or reload of the zfs module; \"Keep ARC limits after reboot\" makes it stay")
	p.Baseline = arcFingerprint(l)
	p.Recheck = func(context.Context) (mutate.Fingerprint, error) {
		l, err := b.R.ARCLimits()
		return arcFingerprint(l), err
	}
	return p, nil
}

func arcFingerprint(l zfs.ARCLimits) mutate.Fingerprint {
	return mutate.Fingerprint{
		"zfs_arc_max": strconv.FormatUint(l.SetMax, 10), "zfs_arc_min": strconv.FormatUint(l.SetMin, 10),
	}
}

// ARCPersist plans writing the limits into /etc/modprobe.d/zfs.conf,
// keeping the old file as zfs.conf.bak. A limit of 0 removes it, which
// means automatic.
func (b Builder) ARCPersist(_ context.Context, maxS, minS string) (*mutate.Plan, error) {
	w, err := parseWish(maxS, minS)
	if err != nil {
		return nil, err
	}
	l, err := b.R.ARCLimits()
	if err != nil {
		return nil, err
	}
	old, exists, err := b.R.Modprobe()
	if err != nil {
		return nil, err
	}
	set := map[string]*uint64{"zfs_arc_max": w.max, "zfs_arc_min": w.min}
	next := modprobeEdit(string(old), set)
	conf := zfs.ModprobeConf
	p := newPlan(mutate.ARCPersist, "Keep the ARC limits after reboot", []string{conf}, map[string]string{"max": maxS, "min": minS})
	blockIf(p, b.Unprivileged, "writing "+conf+" needs root")
	w.check(p, l, false)
	blockIf(p, next == string(old), conf+" already says this")
	if exists {
		p.Steps = append(p.Steps, execute.Argv{Tool: execute.Cp, Options: []string{"-p"}, Operands: []string{conf, conf + ".bak"}})
		affects(p, "the old file is kept as "+conf+".bak")
	}
	tmp := conf + ".fstui-new"
	p.Steps = append(p.Steps,
		execute.Argv{Tool: execute.Tee, Operands: []string{tmp}, Input: []byte(next)},
		execute.Argv{Tool: execute.Mv, Operands: []string{tmp, conf}})
	for _, d := range lineDiff(string(old), next) {
		affects(p, d)
	}
	affects(p, "it applies when the zfs module next loads; if it loads from the initramfs (root on ZFS), rebuild that too (update-initramfs -u on Debian)")
	sum := func(b []byte, ok bool) string {
		if !ok {
			return mutate.Absent
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:8])
	}
	p.Baseline = mutate.Fingerprint{conf: sum(old, exists)}
	p.Recheck = func(context.Context) (mutate.Fingerprint, error) {
		c, ok, err := b.R.Modprobe()
		return mutate.Fingerprint{conf: sum(c, ok)}, err
	}
	return p, nil
}

// modprobeEdit sets or removes (value 0) zfs module options in a
// modprobe.d file, keeping comments and other options: a value on an
// existing "options zfs" line is changed there, a new one goes on a new
// line at the end.
func modprobeEdit(old string, set map[string]*uint64) string {
	done := map[string]bool{}
	var lines []string
	if old != "" {
		lines = strings.Split(strings.TrimSuffix(old, "\n"), "\n")
	}
	for i, line := range lines {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "options" && f[1] == "zfs" {
			lines[i] = editOptions(f, set, done)
		}
	}
	for _, k := range []string{"zfs_arc_max", "zfs_arc_min"} {
		if v := set[k]; v != nil && *v != 0 && !done[k] {
			lines = append(lines, "options zfs "+k+"="+strconv.FormatUint(*v, 10))
		}
	}
	return joinLines(lines)
}

// editOptions rewrites one "options zfs ..." line; empty if nothing is
// left on it. Keys set are marked done.
func editOptions(f []string, set map[string]*uint64, done map[string]bool) string {
	kept := f[:2]
	for _, opt := range f[2:] {
		k, _, _ := strings.Cut(opt, "=")
		v, ok := set[k]
		if !ok || v == nil {
			kept = append(kept, opt)
			continue
		}
		if *v != 0 && !done[k] {
			kept = append(kept, k+"="+strconv.FormatUint(*v, 10))
		}
		done[k] = true
	}
	if len(kept) == 2 {
		return ""
	}
	return strings.Join(kept, " ")
}

// joinLines joins lines into a file, dropping the empty lines an edit
// left at the start, at the end and doubled.
func joinLines(lines []string) string {
	var out []string
	for _, l := range lines {
		if l != "" || len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, l)
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// lineDiff lists lines removed (-) and added (+) between two files.
func lineDiff(old, next string) []string {
	in := func(s, line string) bool {
		for _, l := range strings.Split(s, "\n") {
			if l == line {
				return true
			}
		}
		return false
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(old, "\n"), "\n") {
		if l != "" && !in(next, l) {
			out = append(out, "- "+l)
		}
	}
	for _, l := range strings.Split(strings.TrimSuffix(next, "\n"), "\n") {
		if l != "" && !in(old, l) {
			out = append(out, "+ "+l)
		}
	}
	return out
}

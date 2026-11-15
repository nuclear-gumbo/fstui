// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// These reads serve change previews: what a destroy would remove, what
// holds a snapshot, what is newer than a rollback target. Names are
// validated by the caller.

// tabFields splits zfs -H output into lines of tab-separated fields,
// each with exactly n fields.
func tabFields(out []byte, n int, cmd string) ([][]string, error) {
	var rows [][]string
	for line := range bytes.Lines(out) {
		f := strings.Split(strings.TrimRight(string(line), "\n"), "\t")
		if len(f) != n {
			return nil, fmt.Errorf("%s: unexpected line %q", cmd, line)
		}
		rows = append(rows, f)
	}
	return rows, nil
}

// Get reads the named properties of each object: name, then property,
// then value as zfs get -p prints it ("-" when it does not apply).
func (r *Reader) Get(ctx context.Context, names, props []string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	if len(names) == 0 {
		return out, nil
	}
	data, err := r.read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: []string{"get", "-H", "-p", "-o", "name,property,value", strings.Join(props, ",")}, Operands: names,
	})
	if err != nil {
		return nil, err
	}
	rows, err := tabFields(data, 3, "zfs get")
	if err != nil {
		return nil, err
	}
	for _, f := range rows {
		if out[f[0]] == nil {
			out[f[0]] = map[string]string{}
		}
		out[f[0]][f[1]] = f[2]
	}
	return out, nil
}

// Holds lists the hold tags on each snapshot.
func (r *Reader) Holds(ctx context.Context, snaps []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(snaps) == 0 {
		return out, nil
	}
	data, err := r.read(ctx, execute.Argv{Tool: execute.ZFS, Options: []string{"holds", "-H", "-p"}, Operands: snaps})
	if err != nil {
		return nil, err
	}
	rows, err := tabFields(data, 3, "zfs holds")
	if err != nil {
		return nil, err
	}
	for _, f := range rows {
		out[f[0]] = append(out[f[0]], f[1])
	}
	return out, nil
}

// DestroyPreview is what zfs destroy -nvp reports.
type DestroyPreview struct {
	Destroys []string
	Reclaim  int64 // bytes; -1 when not reported
	Output   string
}

// DestroyDryRun runs zfs destroy -nvp with opts (such as -r) on target.
// It destroys nothing. Eligibility (holds, clones) is not checked by
// it; callers check those themselves.
func (r *Reader) DestroyDryRun(ctx context.Context, opts []string, target string) (DestroyPreview, error) {
	data, err := r.read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: append([]string{"destroy", "-n", "-v", "-p"}, opts...), Operands: []string{target},
	})
	if err != nil {
		return DestroyPreview{}, err
	}
	return ParseDestroyDryRun(data)
}

// ParseDestroyDryRun parses zfs destroy -nvp output.
func ParseDestroyDryRun(data []byte) (DestroyPreview, error) {
	p := DestroyPreview{Reclaim: -1, Output: string(data)}
	rows, err := tabFields(data, 2, "zfs destroy -nvp")
	if err != nil {
		return DestroyPreview{}, err
	}
	for _, f := range rows {
		switch f[0] {
		case "destroy":
			p.Destroys = append(p.Destroys, f[1])
		case "reclaim":
			n, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return DestroyPreview{}, fmt.Errorf("zfs destroy -nvp: reclaim %q: %w", f[1], err)
			}
			p.Reclaim = n
		default:
			return DestroyPreview{}, fmt.Errorf("zfs destroy -nvp: unexpected line %q", strings.Join(f, "\t"))
		}
	}
	return p, nil
}

// Point is a snapshot or bookmark of a dataset, with its creation TXG.
type Point struct {
	Name      string
	CreateTXG uint64
	Clones    []string // snapshots only
}

// PointsOf lists the snapshots and bookmarks of ds (not of its
// children), oldest first.
func (r *Reader) PointsOf(ctx context.Context, ds string) ([]Point, error) {
	data, err := r.read(ctx, execute.Argv{
		Tool:     execute.ZFS,
		Options:  []string{"list", "-H", "-p", "-d", "1", "-t", "snapshot,bookmark", "-o", "name,createtxg,clones", "-s", "createtxg"},
		Operands: []string{ds},
	})
	if err != nil {
		return nil, err
	}
	rows, err := tabFields(data, 3, "zfs list")
	if err != nil {
		return nil, err
	}
	out := make([]Point, 0, len(rows))
	for _, f := range rows {
		txg, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("zfs list: createtxg %q: %w", f[1], err)
		}
		pt := Point{Name: f[0], CreateTXG: txg}
		if f[2] != "-" && f[2] != "" {
			pt.Clones = strings.Split(f[2], ",")
		}
		out = append(out, pt)
	}
	return out, nil
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Identity is what makes a dataset, snapshot or bookmark the object a
// plan was made for: a name can be destroyed and created again, a GUID
// and creation TXG cannot repeat.
type Identity struct {
	GUID      string
	CreateTXG string
}

func (id Identity) String() string { return "guid " + id.GUID + ", createtxg " + id.CreateTXG }

// Identify reads the identity of each named object (datasets, snapshots
// and bookmarks, validated by the caller). Names that do not exist are
// left out of the result; that is an answer, not an error.
func (r *Reader) Identify(ctx context.Context, names []string) (map[string]Identity, error) {
	out := make(map[string]Identity, len(names))
	if len(names) == 0 {
		return out, nil
	}
	res, err := r.run.Read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: []string{"list", "-H", "-p", "-t", "all", "-o", "name,guid,createtxg"}, Operands: names,
	})
	var cmdErr *execute.CommandError
	if err != nil && (!errors.As(err, &cmdErr) || !onlyMissing(cmdErr.Stderr.Raw())) {
		return nil, fmt.Errorf("reading ZFS state: %w", err)
	}
	for line := range bytes.Lines(res.Stdout) {
		f := strings.Split(strings.TrimRight(string(line), "\n"), "\t")
		if len(f) != 3 {
			return nil, fmt.Errorf("zfs list: unexpected line %q", line)
		}
		out[f[0]] = Identity{GUID: f[1], CreateTXG: f[2]}
	}
	return out, nil
}

// onlyMissing reports whether every line of zfs's error output says an
// object does not exist. A missing dataset or snapshot "does not exist";
// a missing bookmark or pool is "no such pool or dataset" (2.3.9).
func onlyMissing(stderr string) bool {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for _, l := range lines {
		missing := strings.HasSuffix(l, "does not exist") || strings.HasSuffix(l, "no such pool or dataset")
		if !strings.HasPrefix(l, "cannot open '") || !missing {
			return false
		}
	}
	return len(lines) > 0
}

// Descendants lists the filesystems and volumes below ds, without ds.
func (r *Reader) Descendants(ctx context.Context, ds string) ([]string, error) {
	out, err := r.read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: []string{"list", "-H", "-r", "-t", "filesystem,volume", "-o", "name"}, Operands: []string{ds},
	})
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range bytes.Lines(out) {
		if n := strings.TrimRight(string(line), "\n"); n != ds {
			names = append(names, n)
		}
	}
	return names, nil
}

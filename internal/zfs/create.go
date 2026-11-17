// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// CreateDryRun runs zpool create -n: it checks the layout and prints the
// pool it would create, without creating it. operands are the pool name
// and the vdev specification.
func (r *Reader) CreateDryRun(ctx context.Context, opts, operands []string) (termtext.Text, error) {
	return r.dryRun(ctx, "create", opts, operands)
}

// dryRun runs zpool verb -n and returns what it prints.
func (r *Reader) dryRun(ctx context.Context, verb string, opts, operands []string) (termtext.Text, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: append([]string{verb, "-n"}, opts...), Operands: operands})
	if err != nil {
		return termtext.Text{}, err
	}
	return termtext.New(string(out)), nil
}

// PoolNames lists the imported pools' names.
func (r *Reader) PoolNames(ctx context.Context) ([]string, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"list", "-H", "-o", "name"}})
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// PoolDevices lists the device paths of every imported pool, by pool:
// the devices a new pool must not use.
func (r *Reader) PoolDevices(ctx context.Context) (map[string][]string, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"status", "-j", "--json-int"}})
	if err != nil {
		return nil, err
	}
	statuses, err := ParsePoolStatus(out)
	if err != nil {
		return nil, err
	}
	pools := map[string][]string{}
	for _, st := range statuses {
		var paths []string
		st.Walk(func(v Vdev) {
			if v.Path != "" {
				paths = append(paths, v.Path)
			}
		})
		pools[st.Name.String()] = paths
	}
	return pools, nil
}

// AddDryRun runs zpool add -n: it checks the new vdevs and prints the
// pool's configuration with them, changing nothing.
func (r *Reader) AddDryRun(ctx context.Context, opts, operands []string) (termtext.Text, error) {
	return r.dryRun(ctx, "add", opts, operands)
}

// RemoveDryRun runs zpool remove -n: it prints the memory the removed
// vdev's mapping would use, changing nothing. It does not check every
// rule a removal has (docs: testdata/zfs/2.3.9/README.md, remove/).
func (r *Reader) RemoveDryRun(ctx context.Context, pool, vdev string) (termtext.Text, error) {
	return r.dryRun(ctx, "remove", nil, []string{pool, vdev})
}

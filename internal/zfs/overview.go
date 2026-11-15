// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"fmt"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Overview is one pool as the Pools view shows it.
type Overview struct {
	Pool   Pool       // zpool list: size, allocation, fragmentation, dedup ratio
	Status PoolStatus // zpool status: devices and their errors, scan, checkpoint
	// Used and Available are the root dataset's: what filesystems can
	// still write. zpool's free overstates that (raidz parity,
	// reservations, slop space), so capacity bars use these.
	Used, Available uint64
}

// rootColumns are read from each pool's root dataset.
const rootColumns = "name,used,available"

// Overviews reads every imported pool with three commands, however many
// pools there are: zpool list, zpool status for all pools, and zfs list
// of the root datasets.
func (r *Reader) Overviews(ctx context.Context) ([]Overview, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"list", "-j", "--json-int"}})
	if err != nil {
		return nil, err
	}
	pools, err := ParsePoolList(out)
	if err != nil || len(pools) == 0 {
		return nil, err
	}
	out, err = r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"status", "-j", "--json-int"}})
	if err != nil {
		return nil, err
	}
	statuses, err := ParsePoolStatus(out)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(pools))
	for i, p := range pools {
		names[i] = p.Name.String()
	}
	out, err = r.read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: []string{"list", "-j", "--json-int", "-d", "0", "-o", rootColumns}, Operands: names,
	})
	if err != nil {
		return nil, err
	}
	roots, err := ParseDatasetList(out)
	if err != nil {
		return nil, err
	}
	return joinOverviews(pools, statuses, roots)
}

// joinOverviews matches the three listings by pool name. A pool missing
// from status or the root listing was exported or destroyed between the
// commands; it is left out rather than shown half-read, and the next
// refresh settles it.
func joinOverviews(pools []Pool, statuses []PoolStatus, roots []Dataset) ([]Overview, error) {
	st := make(map[name.Pool]PoolStatus, len(statuses))
	for _, s := range statuses {
		st[s.Name] = s
	}
	rt := make(map[string]Dataset, len(roots))
	for _, d := range roots {
		rt[d.Name] = d
	}
	out := make([]Overview, 0, len(pools))
	for _, p := range pools {
		s, ok1 := st[p.Name]
		root, ok2 := rt[p.Name.String()]
		if !ok1 || !ok2 {
			continue
		}
		o := Overview{Pool: p, Status: s}
		if err := root.Properties.numbers(map[string]*uint64{"used": &o.Used, "available": &o.Available}); err != nil {
			return nil, fmt.Errorf("pool %s root dataset: %w", p.Name, err)
		}
		out = append(out, o)
	}
	return out, nil
}

// Number returns a numeric property, or false when it is absent or not a
// number ("-", for example fragmentation on a pool without metaslabs).
func (p Properties) Number(prop string) (uint64, bool) {
	v, ok := p[prop]
	return v.Number, ok && v.IsNumber
}

// PoolProperties reads every property of one pool, features included
// (zpool get all). Values keep their source, as zpool get reports it.
func (r *Reader) PoolProperties(ctx context.Context, pool name.Pool) (Pool, error) {
	out, err := r.read(ctx, execute.Argv{
		// -p, not --json-int: OpenZFS 2.3.9 clamps values above 2^63-1 to
		// 9223372036854775807 in --json-int output, which hits half of all
		// GUIDs. -p gives exact digit strings.
		Tool: execute.Zpool, Options: []string{"get", "-j", "-p", "all"}, Operands: []string{pool.String()},
	})
	if err != nil {
		return Pool{}, err
	}
	pools, err := ParsePoolGet(out)
	if err != nil {
		return Pool{}, err
	}
	if len(pools) != 1 || pools[0].Name != pool {
		return Pool{}, fmt.Errorf("zpool get %s: answered for %d pools, want only %s", pool, len(pools), pool)
	}
	return pools[0], nil
}

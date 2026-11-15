// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"fmt"
	"slices"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// datasetColumns are what the Datasets view shows for every filesystem and
// volume: space, compression, mount and encryption state, and the usedby*
// breakdown for the Space tab, in one zfs list for all pools.
const datasetColumns = "name,used,available,referenced,compressratio,mountpoint,encryption,keystatus," +
	"usedbydataset,usedbysnapshots,usedbychildren,usedbyrefreservation,written"

// snapshotDetailColumns add holds and clones to what the storage view needs.
const snapshotDetailColumns = "name,used,referenced,creation,userrefs,clones,written"

// Datasets lists every filesystem and volume in every imported pool,
// without snapshots.
func (r *Reader) Datasets(ctx context.Context) ([]Dataset, error) {
	out, err := r.read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: []string{"list", "-j", "--json-int", "-t", "filesystem,volume", "-o", datasetColumns},
	})
	if err != nil {
		return nil, err
	}
	return ParseDatasetList(out)
}

// PoolDatasets lists the filesystems and volumes of one pool with the same
// columns as Datasets, for the pool's Space tab.
func (r *Reader) PoolDatasets(ctx context.Context, pool name.Pool) ([]Dataset, error) {
	return r.list(ctx, pool.String(), "filesystem,volume", datasetColumns, "-r")
}

// DatasetProperties reads every property of one dataset with its source
// (zfs get all).
func (r *Reader) DatasetProperties(ctx context.Context, ds name.Dataset) (Dataset, error) {
	out, err := r.read(ctx, execute.Argv{
		// -p, not --json-int: see PoolProperties.
		Tool: execute.ZFS, Options: []string{"get", "-j", "-p", "all"}, Operands: []string{ds.String()},
	})
	if err != nil {
		return Dataset{}, err
	}
	all, err := ParseDatasetGet(out)
	if err != nil {
		return Dataset{}, err
	}
	if len(all) != 1 || all[0].Name != ds.String() {
		return Dataset{}, fmt.Errorf("zfs get %s: answered for %d datasets, want only %s", ds, len(all), ds)
	}
	return all[0], nil
}

// DatasetSnapshots lists the snapshots of one dataset, not of its
// children, oldest first, with their holds and dependent clones.
func (r *Reader) DatasetSnapshots(ctx context.Context, ds name.Dataset) ([]Dataset, error) {
	snaps, err := r.list(ctx, ds.String(), "snapshot", snapshotDetailColumns, "-d", "1")
	if err != nil {
		return nil, err
	}
	sortByTXG(snaps)
	return snaps, nil
}

// sortByTXG orders snapshots exactly; creation has one-second resolution
// and ties for snapshots taken in the same second.
func sortByTXG(ds []Dataset) {
	slices.SortStableFunc(ds, func(a, b Dataset) int {
		switch {
		case a.CreateTXG < b.CreateTXG:
			return -1
		case a.CreateTXG > b.CreateTXG:
			return 1
		}
		return 0
	})
}

// allSnapshotColumns are what the global Snapshots view shows.
const allSnapshotColumns = "name,used,referenced,creation,userrefs,clones"

// volumeColumns are what the Volumes view shows: size, block size, and
// refreservation, which is 0 for a sparse volume (zfs create -s).
const volumeListColumns = "name,volsize,used,referenced,volblocksize,refreservation,compressratio,encryption,keystatus"

// AllSnapshots lists every snapshot in every pool, oldest first. This can
// be hundreds of thousands of entries, so it is read on request only.
func (r *Reader) AllSnapshots(ctx context.Context) ([]Dataset, error) {
	out, err := r.read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: []string{"list", "-j", "--json-int", "-t", "snapshot", "-o", allSnapshotColumns},
	})
	if err != nil {
		return nil, err
	}
	snaps, err := ParseDatasetList(out)
	if err != nil {
		return nil, err
	}
	sortByTXG(snaps)
	return snaps, nil
}

// AllVolumes lists every volume (zvol) in every pool, sorted by name.
func (r *Reader) AllVolumes(ctx context.Context) ([]Dataset, error) {
	out, err := r.read(ctx, execute.Argv{
		Tool: execute.ZFS, Options: []string{"list", "-j", "--json-int", "-t", "volume", "-o", volumeListColumns},
	})
	if err != nil {
		return nil, err
	}
	return ParseDatasetList(out)
}

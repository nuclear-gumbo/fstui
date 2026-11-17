// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// datasetColumns are what the Datasets view shows for every filesystem and
// volume: space, compression, mount and encryption state, and the usedby*
// breakdown for the Space tab, in one zfs list for all pools.
const datasetColumns = "name,used,available,referenced,compressratio,mountpoint,encryption,keystatus," +
	"usedbydataset,usedbysnapshots,usedbychildren,usedbyrefreservation,written"

// snapshotDetailColumns add holds and clones to what the storage view needs.

// Datasets lists every filesystem and volume in every imported pool,
// without snapshots.
func (r *Reader) Datasets(ctx context.Context) ([]Dataset, error) {
	return r.listDatasets(ctx, []string{"-t", "filesystem,volume", "-o", datasetColumns}, nil)
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

// SnapshotRow is one snapshot as the snapshot lists show it. It is a plain
// struct rather than a Dataset with a property map: a system can hold
// hundreds of thousands, and the map costs eight times the memory.
type SnapshotRow struct {
	Name                                         string
	CreateTXG, Creation, Used, Referenced, Holds uint64
	Clones                                       termtext.Text // comma-separated, "" without clones
}

// DatasetSnapshots lists the snapshots of one dataset, not of its
// children, oldest first, with their holds and dependent clones.
func (r *Reader) DatasetSnapshots(ctx context.Context, ds name.Dataset) ([]SnapshotRow, error) {
	return r.snapshots(ctx, []string{"-d", "1"}, []string{ds.String()})
}

// allSnapshotColumns are what the global Snapshots view shows.
const allSnapshotColumns = "name,used,referenced,creation,userrefs,clones"

// volumeColumns are what the Volumes view shows: size, block size, and
// refreservation, which is 0 for a sparse volume (zfs create -s).
const volumeListColumns = "name,volsize,used,referenced,volblocksize,refreservation,compressratio,encryption,keystatus"

// AllSnapshots lists every snapshot in every pool, oldest first. This can
// be hundreds of thousands of entries, so it is read on request only.
func (r *Reader) AllSnapshots(ctx context.Context) ([]SnapshotRow, error) {
	return r.snapshots(ctx, nil, nil)
}

func (r *Reader) snapshots(ctx context.Context, opts, operands []string) ([]SnapshotRow, error) {
	a := execute.Argv{
		Tool: execute.ZFS, Options: append([]string{"list", "-j", "--json-int", "-t", "snapshot", "-o", allSnapshotColumns}, opts...), Operands: operands,
	}
	var out []SnapshotRow
	err := r.readTo(ctx, a, func(rd io.Reader) error {
		var err error
		out, err = parseSnapshots(rd)
		return err
	})
	return out, err
}

// ParseSnapshotList parses zfs list -j --json-int -t snapshot -o
// name,used,referenced,creation,userrefs,clones, oldest first: by
// creation TXG, since creation has one-second resolution.
func ParseSnapshotList(data []byte) ([]SnapshotRow, error) {
	return parseSnapshots(bytes.NewReader(data))
}

func parseSnapshots(rd io.Reader) ([]SnapshotRow, error) {
	var out []SnapshotRow
	err := decodeEachFrom(rd, "zfs list", "datasets", func(jd jsonDataset) error {
		if err := validateDatasetName(jd.Name, jd.Type); err != nil || jd.Type != Snapshot {
			return fmt.Errorf("zfs list: snapshot %s: %w", termtext.New(jd.Name), cmp.Or(err, errors.New("not a snapshot")))
		}
		s := SnapshotRow{Name: jd.Name, CreateTXG: uint64(jd.CreateTXG)}
		for prop, dst := range map[string]*uint64{"creation": &s.Creation, "used": &s.Used, "referenced": &s.Referenced, "userrefs": &s.Holds} {
			p, err := jd.Properties[prop].parse(prop)
			if err != nil || !p.IsNumber {
				return fmt.Errorf("zfs list: snapshot %s: %s is not a number", termtext.New(jd.Name), prop)
			}
			*dst = p.Number
		}
		if c, ok := jd.Properties["clones"]; ok {
			p, err := c.parse("clones")
			if err != nil {
				return fmt.Errorf("zfs list: snapshot %s: %w", termtext.New(jd.Name), err)
			}
			if t := p.Text.Raw(); t != "-" {
				s.Clones = p.Text
			}
		}
		out = append(out, s)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b SnapshotRow) int {
		return cmp.Or(cmp.Compare(a.CreateTXG, b.CreateTXG), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// AllVolumes lists every volume (zvol) in every pool, sorted by name.
func (r *Reader) AllVolumes(ctx context.Context) ([]Dataset, error) {
	return r.listDatasets(ctx, []string{"-t", "volume", "-o", volumeListColumns}, nil)
}

// listDatasets runs zfs list -j --json-int with opts and parses the
// listing as it arrives.
func (r *Reader) listDatasets(ctx context.Context, opts, operands []string) ([]Dataset, error) {
	a := execute.Argv{Tool: execute.ZFS, Options: append([]string{"list", "-j", "--json-int"}, opts...), Operands: operands}
	var out []Dataset
	err := r.readTo(ctx, a, func(rd io.Reader) error {
		var err error
		out, err = parseDatasets(rd, "zfs list")
		return err
	})
	return out, err
}

// PoolObjects lists every filesystem, volume and snapshot of one pool
// with when it was created and where it is mounted: what a checkpoint
// rewind would take away, and whether the pool holds the system's root.
func (r *Reader) PoolObjects(ctx context.Context, pool name.Pool) ([]Dataset, error) {
	return r.list(ctx, pool.String(), "filesystem,volume,snapshot", "name,creation,mountpoint,mounted", "-r")
}

// sortByTXG orders snapshots exactly; creation has one-second resolution
// and ties for snapshots taken in the same second.
func sortByTXG(ds []Dataset) {
	slices.SortStableFunc(ds, func(a, b Dataset) int { return cmp.Compare(a.CreateTXG, b.CreateTXG) })
}

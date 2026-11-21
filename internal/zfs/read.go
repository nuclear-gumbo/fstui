// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/storage"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Columns requested from zfs list. Each query asks only for what its
// neutral type carries.
const (
	filesystemColumns = "name,used,available,referenced,mountpoint"
	volumeColumns     = "name,used,available,referenced,volsize"
	snapshotColumns   = "name,used,referenced,creation"
)

// Reader reads ZFS state through zfs and zpool. It implements the storage
// reader interfaces.
type Reader struct {
	run Commands
	// CronFile is Debian's scrub cron file, read for ScrubSchedules;
	// empty to ignore cron (the demo build, and tests).
	CronFile string
	// ReadFile, if set, reads module parameters, ARC statistics and
	// /etc/modprobe.d/zfs.conf instead of the host's files (the demo).
	ReadFile fileReader
	// ReadDir, if set, lists a directory's entries instead of the host's
	// (the demo); tune mode lists a pool's kstat directory with it.
	ReadDir func(string) ([]string, error)
	// HistoryKeep and DiffMax bound pool history and zfs diff
	// (limits.history_entries, limits.diff_entries); defaults when zero.
	HistoryKeep, DiffMax int
	// HistoryTimeout bounds zpool history (limits.history_timeout);
	// defaultHistoryTimeout when zero.
	HistoryTimeout time.Duration
}

// Commands runs zfs and zpool for a Reader. *execute.Runner implements
// it; the demo build (tag fstui_demo) replays recorded output instead.
type Commands interface {
	Read(ctx context.Context, a execute.Argv) (execute.Result, error)
	Stream(ctx context.Context, a execute.Argv, line func([]byte)) error
}

// NewReader returns a Reader that runs commands with run, normally an
// *execute.Runner that has resolved execute.ZFS and execute.Zpool.
func NewReader(run Commands) *Reader { return &Reader{run: run, CronFile: DebianScrubCron} }

var (
	_ storage.PoolReader       = (*Reader)(nil)
	_ storage.PoolStatusReader = (*Reader)(nil)
	_ storage.FilesystemReader = (*Reader)(nil)
	_ storage.VolumeReader     = (*Reader)(nil)
	_ storage.SnapshotReader   = (*Reader)(nil)
)

// readerTo is a Commands that can hand output over as it arrives
// (*execute.Runner); others are read whole.
type readerTo interface {
	ReadTo(ctx context.Context, a execute.Argv, consume func(io.Reader) error) error
}

// readTo runs a and hands its output to consume, streamed when run can.
func (r *Reader) readTo(ctx context.Context, a execute.Argv, consume func(io.Reader) error) error {
	if rt, ok := r.run.(readerTo); ok {
		return rt.ReadTo(ctx, a, consume) // execute names the command
	}
	out, err := r.read(ctx, a)
	if err != nil {
		return err
	}
	return consume(bytes.NewReader(out))
}

func (r *Reader) read(ctx context.Context, a execute.Argv) ([]byte, error) {
	res, err := r.run.Read(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("reading ZFS state: %w", err)
	}
	return res.Stdout, nil
}

// Pools lists every imported pool, sorted by name.
func (r *Reader) Pools(ctx context.Context) ([]storage.Pool, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"list", "-j", "--json-int"}})
	if err != nil {
		return nil, err
	}
	pools, err := ParsePoolList(out)
	if err != nil {
		return nil, err
	}
	return neutralPools(pools)
}

// PoolStatus reads the device tree and scan state of one pool.
func (r *Reader) PoolStatus(ctx context.Context, pool storage.Pool) (storage.PoolStatus, error) {
	p, err := name.ParsePool(pool.Name.Raw())
	if err != nil {
		return storage.PoolStatus{}, fmt.Errorf("pool %s: %w", pool.Name, err)
	}
	out, err := r.read(ctx, execute.Argv{
		Tool: execute.Zpool, Options: []string{"status", "-j", "--json-int"}, Operands: []string{p.String()},
	})
	if err != nil {
		return storage.PoolStatus{}, err
	}
	st, err := ParsePoolStatus(out)
	if err != nil {
		return storage.PoolStatus{}, err
	}
	if len(st) != 1 || st[0].Name != p {
		return storage.PoolStatus{}, fmt.Errorf("zpool status %s: answered for %d pools, want only %s", p, len(st), p)
	}
	return neutralStatus(st[0])
}

// Filesystems lists the filesystems of a pool, parents before children.
func (r *Reader) Filesystems(ctx context.Context, pool storage.Pool) ([]storage.Filesystem, error) {
	ds, err := r.listPool(ctx, pool, "filesystem", filesystemColumns)
	if err != nil {
		return nil, err
	}
	out := make([]storage.Filesystem, 0, len(ds))
	for _, d := range ds {
		fs := storage.Filesystem{Name: termtext.New(d.Name), Mountpoint: d.Properties["mountpoint"].Text}
		if err := d.Properties.numbers(map[string]*uint64{
			"used": &fs.Used, "available": &fs.Available, "referenced": &fs.Referenced,
		}); err != nil {
			return nil, fmt.Errorf("filesystem %s: %w", fs.Name, err)
		}
		out = append(out, fs)
	}
	return out, nil
}

// Volumes lists the volumes (zvols) of a pool, sorted by name.
func (r *Reader) Volumes(ctx context.Context, pool storage.Pool) ([]storage.Volume, error) {
	ds, err := r.listPool(ctx, pool, "volume", volumeColumns)
	if err != nil {
		return nil, err
	}
	out := make([]storage.Volume, 0, len(ds))
	for _, d := range ds {
		v := storage.Volume{Name: termtext.New(d.Name)}
		if err := d.Properties.numbers(map[string]*uint64{
			"volsize": &v.Size, "used": &v.Used, "available": &v.Available, "referenced": &v.Referenced,
		}); err != nil {
			return nil, fmt.Errorf("volume %s: %w", v.Name, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// Snapshots lists the snapshots of one filesystem, not of its children,
// oldest first.
func (r *Reader) Snapshots(ctx context.Context, fs storage.Filesystem) ([]storage.Snapshot, error) {
	d, err := name.ParseDataset(fs.Name.Raw())
	if err != nil {
		return nil, fmt.Errorf("filesystem %s: %w", fs.Name, err)
	}
	ds, err := r.list(ctx, d.String(), "snapshot", snapshotColumns, "-d", "1")
	if err != nil {
		return nil, err
	}
	return neutralSnapshots(ds)
}

func (r *Reader) listPool(ctx context.Context, pool storage.Pool, typ, columns string) ([]Dataset, error) {
	p, err := name.ParsePool(pool.Name.Raw())
	if err != nil {
		return nil, fmt.Errorf("pool %s: %w", pool.Name, err)
	}
	return r.list(ctx, p.String(), typ, columns, "-r")
}

func (r *Reader) list(ctx context.Context, operand, typ, columns string, depth ...string) ([]Dataset, error) {
	return r.listDatasets(ctx, append([]string{"-t", typ, "-o", columns}, depth...), []string{operand})
}

// numbers copies numeric properties into the given fields. A missing or
// non-numeric value is an error: with --json-int every byte count is a
// JSON number, so anything else means the output is not what fstui asked for.
func (p Properties) numbers(dst map[string]*uint64) error {
	for prop, field := range dst {
		v, ok := p[prop]
		if !ok {
			return fmt.Errorf("property %s missing", prop)
		}
		if !v.IsNumber {
			return fmt.Errorf("property %s is %q, not a number", prop, v.Text)
		}
		*field = v.Number
	}
	return nil
}

func neutralPools(pools []Pool) ([]storage.Pool, error) {
	out := make([]storage.Pool, 0, len(pools))
	for _, p := range pools {
		sp := storage.Pool{Name: termtext.New(p.Name.String()), Health: health(p.State)}
		if err := p.Properties.numbers(map[string]*uint64{
			"size": &sp.Size, "allocated": &sp.Allocated, "free": &sp.Free,
		}); err != nil {
			return nil, fmt.Errorf("pool %s: %w", p.Name, err)
		}
		out = append(out, sp)
	}
	return out, nil
}

func neutralStatus(p PoolStatus) (storage.PoolStatus, error) {
	st := storage.PoolStatus{Health: health(p.State), Problem: p.Status, Action: p.Action}
	// The root of the data tree is the pool itself; its children are the
	// top-level devices. The other groups list top-level devices directly.
	for _, g := range []struct {
		role  storage.Role
		vdevs []Vdev
	}{
		{storage.Data, p.Data.Children},
		{storage.Metadata, p.Special},
		{storage.Dedup, p.Dedup},
		{storage.Log, p.Logs},
		{storage.Cache, p.Cache},
		{storage.Spare, p.Spares},
	} {
		for _, v := range g.vdevs {
			st.Devices = append(st.Devices, neutralDevice(v, g.role))
		}
	}
	if p.Scan != nil {
		sc, err := neutralScan(*p.Scan)
		if err != nil {
			return storage.PoolStatus{}, fmt.Errorf("pool %s: %w", p.Name, err)
		}
		st.Scan = &sc
	}
	return st, nil
}

func neutralDevice(v Vdev, role storage.Role) storage.Device {
	d := storage.Device{
		Name: v.Name, Kind: v.Type, Role: role, Health: health(v.State),
		ReadErrors: v.ReadErrors, WriteErrors: v.WriteErrors, ChecksumErrors: v.ChecksumErrors,
	}
	for _, c := range v.Children {
		d.Children = append(d.Children, neutralDevice(c, role))
	}
	return d
}

// neutralScan maps scan_stats. ZFS scans in two passes, examining metadata
// and then issuing the reads; zpool status computes "% done" from the
// issued bytes, so Done is Issued, not Examined.
func neutralScan(s Scan) (storage.Scan, error) {
	sc := storage.Scan{
		Word: s.Function, Start: s.Start, End: s.End,
		Total: s.ToExamine, Done: s.Issued, Errors: s.Errors,
	}
	switch {
	case s.State == "SCANNING" && s.Paused:
		sc.State = storage.Paused
	case s.State == "SCANNING":
		sc.State = storage.Running
	case s.State == "FINISHED":
		sc.State = storage.Finished
	case s.State == "CANCELED":
		sc.State = storage.Canceled
	default:
		return storage.Scan{}, fmt.Errorf("scan state %q is not one fstui knows", s.State)
	}
	return sc, nil
}

func neutralSnapshots(ds []Dataset) ([]storage.Snapshot, error) {
	sortByTXG(ds)
	out := make([]storage.Snapshot, 0, len(ds))
	for _, d := range ds {
		s := storage.Snapshot{Name: termtext.New(d.Name)}
		var created uint64
		if err := d.Properties.numbers(map[string]*uint64{
			"used": &s.Used, "referenced": &s.Referenced, "creation": &created,
		}); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", s.Name, err)
		}
		s.Created = time.Unix(int64(created), 0) // seconds since 1970 fit in int64
		out = append(out, s)
	}
	return out, nil
}

// health maps the state words of zpoolconcepts(7). Spares report AVAIL or
// INUSE instead; both mean the spare itself is fine.
func health(word string) storage.Health {
	c := storage.Unknown
	switch word {
	case "ONLINE", "AVAIL", "INUSE":
		c = storage.Online
	case "DEGRADED":
		c = storage.Degraded
	case "OFFLINE":
		c = storage.Offline
	case "REMOVED":
		c = storage.Removed
	case "UNAVAIL":
		c = storage.Unavailable
	case "FAULTED":
		c = storage.Faulted
	}
	return storage.Health{Condition: c, Word: word}
}

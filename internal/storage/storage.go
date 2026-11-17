// SPDX-License-Identifier: GPL-3.0-or-later

// Package storage is the backend-neutral vocabulary the generic views use:
// pools, devices, filesystems, block volumes, snapshots and integrity scans.
// It holds only what several filesystems share; anything specific to one
// backend stays in that backend's package.
//
// A backend offers a capability by implementing its reader interface.
// Views find out what a backend supports with a type assertion, so a
// backend without block volumes simply has no Volumes method and its
// volume view is not shown.
package storage

import (
	"context"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Condition is the health of a pool or device, as a closed set the UI can
// colour and warn on. The backend's own word for it travels alongside in
// Health.Word, because that is what users search documentation for.
type Condition int

// Conditions, from best to worst.
const (
	Online Condition = iota + 1
	Degraded
	Offline
	Removed
	Unavailable
	Faulted
	// Unknown is a state word the backend printed that fstui does not know.
	// Views treat it as a problem, since it is not known to be fine.
	Unknown
)

// Health is a condition and the word the backend used for it, e.g.
// {Degraded, "DEGRADED"}.
type Health struct {
	Condition Condition
	Word      string
}

// OK reports whether nothing needs attention.
func (h Health) OK() bool { return h.Condition == Online }

// Pool is a storage pool: a set of devices that filesystems allocate from.
type Pool struct {
	Name      termtext.Text
	Health    Health
	Size      uint64 // bytes of raw capacity, before redundancy
	Allocated uint64 // bytes allocated on the devices, including redundancy
	Free      uint64 // Size minus Allocated; not the space filesystems can still write
}

// Role is what a device contributes to its pool.
type Role int

// Device roles.
const (
	Data Role = iota + 1
	// Metadata devices hold metadata and small blocks on faster media.
	Metadata
	Dedup
	Log
	Cache
	Spare
)

// Device is a node in a pool's device tree: a physical device or file, or
// a group of them with redundancy (a mirror, a parity group).
type Device struct {
	Name           termtext.Text
	Kind           string // the backend's word, e.g. "mirror", "disk", "file"
	Role           Role
	Health         Health
	ReadErrors     uint64
	WriteErrors    uint64
	ChecksumErrors uint64
	Children       []Device
}

// ScanState is the state of an integrity scan.
type ScanState int

// Scan states.
const (
	Running ScanState = iota + 1
	Paused
	Finished
	Canceled
)

// Scan is the current or most recent integrity scan of a pool: a check of
// every block against its checksum, or a rebuild onto a new device.
type Scan struct {
	Word   string // the backend's name for the scan, e.g. "SCRUB", "RESILVER"
	State  ScanState
	Start  time.Time
	End    time.Time // zero until the scan finishes or is cancelled
	Total  uint64    // bytes to process
	Done   uint64    // bytes processed so far
	Errors uint64    // errors found that could not be repaired
}

// PoolStatus is the detailed state of one pool.
type PoolStatus struct {
	Health  Health
	Problem []termtext.Text // the backend's explanation of a problem, one line each
	Action  []termtext.Text // what the backend recommends doing about it
	Devices []Device        // top-level devices in backend order, with their roles
	Scan    *Scan           // nil if the pool was never scanned
}

// Filesystem is a mountable filesystem within a pool.
type Filesystem struct {
	Name       termtext.Text
	Used       uint64 // bytes used by the filesystem and everything below it
	Available  uint64 // bytes it can still write
	Referenced uint64 // bytes of data reachable from the filesystem itself
	Mountpoint termtext.Text
}

// Volume is a block device backed by a pool.
type Volume struct {
	Name       termtext.Text
	Size       uint64 // logical size of the block device
	Used       uint64
	Available  uint64
	Referenced uint64
}

// Snapshot is a read-only point-in-time copy of a filesystem.
type Snapshot struct {
	Name       termtext.Text
	Created    time.Time
	Used       uint64 // bytes that destroying only this snapshot would free
	Referenced uint64
}

// PoolReader lists pools.
type PoolReader interface {
	Pools(ctx context.Context) ([]Pool, error)
}

// PoolStatusReader reads a pool's device tree, problems and scan state,
// which every backend that has them reports together.
type PoolStatusReader interface {
	PoolStatus(ctx context.Context, pool Pool) (PoolStatus, error)
}

// FilesystemReader lists a pool's filesystems, parents before children.
type FilesystemReader interface {
	Filesystems(ctx context.Context, pool Pool) ([]Filesystem, error)
}

// VolumeReader lists a pool's block volumes.
type VolumeReader interface {
	Volumes(ctx context.Context, pool Pool) ([]Volume, error)
}

// SnapshotReader lists the snapshots of one filesystem, oldest first.
type SnapshotReader interface {
	Snapshots(ctx context.Context, fs Filesystem) ([]Snapshot, error)
}

// SPDX-License-Identifier: GPL-3.0-or-later

package disks

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Runner runs a read-only command; *execute.Runner is one.
type Runner interface {
	Read(ctx context.Context, a execute.Argv) (execute.Result, error)
}

// Source reads the device inventory. ByIDDir and SysBlock are
// /dev/disk/by-id and /sys/class/block on a real system; empty skips
// them (the demo, tests). PoolDevices lists the device paths of imported
// pools, by pool.
type Source struct {
	Run         Runner
	ByIDDir     string
	SysBlock    string
	PoolDevices func(ctx context.Context) (map[string][]string, error)
}

// Devices lists every disk and partition with what Judge needs to know.
func (s Source) Devices(ctx context.Context) ([]Device, error) {
	res, err := s.Run.Read(ctx, execute.Argv{Tool: execute.Lsblk, Options: LsblkArgs})
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	ds, err := ParseLsblk(res.Stdout)
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	if s.ByIDDir != "" {
		if ids, err = ByID(s.ByIDDir); err != nil {
			return nil, err
		}
	}
	inPool, err := s.poolMembers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range ds {
		d := &ds[i]
		d.ByID = d.Path
		if id, ok := ids[d.Name]; ok {
			d.ByID = id
		}
		if s.SysBlock != "" {
			d.Held = Held(s.SysBlock, d.Name)
		}
		d.InPool = inPool[d.Name]
	}
	markParents(ds)
	return ds, nil
}

// poolMembers maps kernel names to the imported pool using them.
func (s Source) poolMembers(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	if s.PoolDevices == nil {
		return out, nil
	}
	pools, err := s.PoolDevices(ctx)
	if err != nil {
		return nil, err
	}
	for pool, paths := range pools {
		for _, p := range paths {
			out[kernelName(p)] = pool
		}
	}
	return out, nil
}

// markParents puts a disk in the pool one of its partitions is in.
func markParents(ds []Device) {
	for i := range ds {
		if ds[i].InPool != "" {
			continue
		}
		for _, p := range ds {
			if p.Parent == ds[i].Name && p.InPool != "" {
				ds[i].InPool = p.InPool
			}
		}
	}
}

// kernelName is the device name a path resolves to: sda1 for
// /dev/disk/by-id/ata-X-part1. A path that does not resolve keeps its
// own base name.
func kernelName(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	return filepath.Base(path)
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"math/bits"
	"strconv"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
)

// VolumeCreate plans "zfs create [-s] -V size [-b block] ds". A sparse
// volume reserves nothing; a thick one reserves its size.
func (b Builder) VolumeCreate(ctx context.Context, ds, size string, sparse bool, block string) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	bytes, err := ParseSize(size)
	if err != nil || bytes == 0 {
		return nil, fmt.Errorf("size: %s is not a size above zero (e.g. 20G)", show(size))
	}
	opts := []string{"create"}
	if sparse {
		opts = append(opts, "-s")
	}
	opts = append(opts, "-V", strconv.FormatUint(bytes, 10))
	if block != "" {
		n, err := ParseSize(block)
		if err != nil || bits.OnesCount64(n) != 1 || n < 512 || n > 128<<10 {
			return nil, fmt.Errorf("block size: %s is not a power of two from 512 bytes to 128K", show(block))
		}
		if bytes%n != 0 {
			return nil, fmt.Errorf("size: %s is not a multiple of the block size %s", show(size), show(block))
		}
		opts = append(opts, "-b", strconv.FormatUint(n, 10))
	}
	p := newPlan(mutate.VolumeCreate, "Create volume "+show(ds)+" of "+iec(bytes), []string{ds},
		map[string]string{"dataset": ds, "size": size, "sparse": boolParam(sparse), "block": block},
		zfsArgv(opts, ds))
	affects(p, "creates the block device /dev/zvol/"+ds)
	if sparse {
		affects(p, "no space is reserved: writes to it fail if the pool fills up")
	} else {
		affects(p, iec(bytes)+" of the pool is reserved for it now")
	}
	parent, hasParent := parentOf(ds)
	names := []string{ds}
	if hasParent {
		names = append(names, parent)
	}
	if err := b.watch(ctx, p, names); err != nil {
		return nil, err
	}
	blockIf(p, !hasParent, "a volume cannot be a pool's root dataset")
	blockIf(p, p.Baseline[ds] != mutate.Absent, show(ds)+" already exists")
	blockIf(p, hasParent && p.Baseline[parent] == mutate.Absent, show(parent)+" does not exist")
	if hasParent {
		return p, b.checkParent(ctx, p, parent, "create", "mount")
	}
	return p, nil
}

// VolumeResize plans "zfs set volsize=N ds". Shrinking cuts off the
// data past the new size.
func (b Builder) VolumeResize(ctx context.Context, ds, size string) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	bytes, err := ParseSize(size)
	if err != nil || bytes == 0 {
		return nil, fmt.Errorf("size: %s is not a size above zero (e.g. 20G)", show(size))
	}
	facts, err := b.R.Get(ctx, []string{ds}, []string{"volsize", "volblocksize", "type"})
	if err != nil {
		return nil, err
	}
	f := facts[ds]
	if f["type"] != "volume" {
		return nil, fmt.Errorf("%s is not a volume", show(ds))
	}
	cur, _ := strconv.ParseUint(f["volsize"], 10, 64)
	blk, _ := strconv.ParseUint(f["volblocksize"], 10, 64)
	shrink := bytes < cur
	verb := "Grow"
	if shrink {
		verb = "Shrink"
	}
	p := newPlan(mutate.VolumeResize, fmt.Sprintf("%s %s from %s to %s", verb, show(ds), iec(cur), iec(bytes)), []string{ds},
		map[string]string{"dataset": ds, "size": size, "shrink": boolParam(shrink)},
		zfsArgv([]string{"set"}, "volsize="+strconv.FormatUint(bytes, 10), ds))
	p.Property = "volsize"
	if shrink {
		p.Irreversible = []string{"Everything stored past " + iec(bytes) + " in the volume is lost; shrink the filesystem or partition inside it first."}
	} else {
		affects(p, "whatever uses the volume (a virtual machine, a filesystem on it) must be told to use the new space")
	}
	blockIf(p, bytes == cur, "it is already "+iec(cur))
	blockIf(p, blk > 0 && bytes%blk != 0, "the new size must be a multiple of the block size "+iec(blk))
	return p, b.watchWith(ctx, p, []string{ds}, func(ctx context.Context) (mutate.Fingerprint, error) {
		g, err := b.R.Get(ctx, []string{ds}, []string{"volsize"})
		if err != nil {
			return nil, err
		}
		return mutate.Fingerprint{ds + " (volsize)": g[ds]["volsize"]}, nil
	})
}

// iec formats a byte count in IEC units for summaries: 20 GiB, 1.5 TiB.
func iec(n uint64) string {
	if n < 1024 {
		return strconv.FormatUint(n, 10) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	f := float64(n) / 1024
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	s := strconv.FormatFloat(f, 'f', 1, 64)
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s + " " + units[i]
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Files the ARC limit changes read and write.
const (
	ParamDir     = "/sys/module/zfs/parameters"
	ModprobeConf = "/etc/modprobe.d/zfs.conf"
)

// ARCLimits are the ARC's limits as set (0 means automatic) and as in
// effect, with what it holds now and the memory it can use.
type ARCLimits struct {
	SetMax, SetMin uint64 // zfs_arc_max, zfs_arc_min
	Stats          ARCStats
}

// fileReader reads a file; the demo build replaces it so its reads
// never reach the host's files.
type fileReader func(string) ([]byte, error)

func (r *Reader) file(path string) ([]byte, error) {
	if r.ReadFile != nil {
		return r.ReadFile(path)
	}
	return os.ReadFile(path) //nolint:gosec,wrapcheck // fixed paths named in this file; callers add context
}

// ARCLimits reads the module parameters and the ARC statistics.
func (r *Reader) ARCLimits() (ARCLimits, error) {
	var l ARCLimits
	for name, dst := range map[string]*uint64{"zfs_arc_max": &l.SetMax, "zfs_arc_min": &l.SetMin} {
		b, err := r.file(ParamDir + "/" + name)
		if err != nil {
			return ARCLimits{}, fmt.Errorf("reading %s: %w", name, err)
		}
		if *dst, err = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err != nil {
			return ARCLimits{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	b, err := r.file(ARCStatsPath)
	if err != nil {
		return ARCLimits{}, fmt.Errorf("reading ARC statistics: %w", err)
	}
	if l.Stats, err = ParseARCStats(b); err != nil {
		return ARCLimits{}, err
	}
	return l, nil
}

// Modprobe reads /etc/modprobe.d/zfs.conf; a missing file is empty and
// exists is false.
func (r *Reader) Modprobe() (content []byte, exists bool, err error) {
	b, err := r.file(ModprobeConf)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", ModprobeConf, err)
	}
	return b, true, nil
}

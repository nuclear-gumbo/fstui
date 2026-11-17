// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// KstatDir is where OpenZFS's kstats are. Every user can read them.
const KstatDir = "/proc/spl/kstat/zfs"

// TuneSample is one reading of the counters tune mode compares: per
// dataset (by name) its objset kstat, the pool's transaction groups, and
// the global ARC, DMU transaction, ZIL and prefetch kstats. Counters count
// since the pool was imported or the module loaded; tune mode uses the
// difference between two samples.
type TuneSample struct {
	At                      time.Time
	Objsets                 map[string]Kstat
	Txgs                    []Txg
	ARC, DMUTx, ZIL, Zfetch Kstat
}

// ReadTuneSample reads pool's kstats and the global ones.
func (r *Reader) ReadTuneSample(pool name.Pool, now time.Time) (TuneSample, error) {
	s := TuneSample{At: now, Objsets: map[string]Kstat{}}
	dir := path.Join(KstatDir, pool.String())
	entries, err := r.dir(dir)
	if err != nil {
		return TuneSample{}, fmt.Errorf("listing %s: %w", dir, err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e, "objset-0x") {
			continue
		}
		k, err := r.kstat(path.Join(dir, e))
		if err != nil {
			continue // a dataset destroyed between the listing and the read
		}
		if n := k.String("dataset_name"); n != "" {
			s.Objsets[n] = k
		}
	}
	b, err := r.file(path.Join(dir, "txgs"))
	if err != nil {
		return TuneSample{}, fmt.Errorf("reading %s transaction groups: %w", pool, err)
	}
	if s.Txgs, err = ParseTxgs(b); err != nil {
		return TuneSample{}, err
	}
	for name, dst := range map[string]*Kstat{"arcstats": &s.ARC, "dmu_tx": &s.DMUTx, "zil": &s.ZIL, "zfetchstats": &s.Zfetch} {
		if *dst, err = r.kstat(path.Join(KstatDir, name)); err != nil {
			return TuneSample{}, err
		}
	}
	return s, nil
}

func (r *Reader) kstat(p string) (Kstat, error) {
	b, err := r.file(p)
	if err != nil {
		return Kstat{}, fmt.Errorf("reading %s: %w", p, err)
	}
	return ParseKstat(b)
}

func (r *Reader) dir(p string) ([]string, error) {
	if r.ReadDir != nil {
		return r.ReadDir(p)
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the directory
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out, nil
}

// TuneIO streams zpool iostat -l for pool, one interval of every at a
// time, until ctx ends: the pool's rates and latencies and its devices'.
func (r *Reader) TuneIO(ctx context.Context, pool name.Pool, every time.Duration, report func(IOPool)) error {
	secs := strconv.Itoa(max(int(every/time.Second), 1))
	a := execute.Argv{Tool: execute.Zpool, Options: IOStatArgv.Options, Operands: []string{pool.String(), secs}}
	err := r.streamIOStat(ctx, a, func(s IOSample) {
		for _, p := range s {
			if p.Name.Raw() == pool.String() {
				report(p)
			}
		}
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("reading %s I/O: %w", pool, err)
	}
	return nil
}

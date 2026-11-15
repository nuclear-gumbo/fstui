// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// fakeReader holds the objects that exist and what the reads return.
type fakeReader struct {
	ids    map[string]zfs.Identity
	kids   map[string][]string
	props  map[string]map[string]string
	holds  map[string][]string
	dry    map[string]zfs.DestroyPreview // by "opts target"
	dryErr map[string]error
	points map[string][]zfs.Point
	pools  map[string]zfs.Activity
	topo   map[string]zfs.PoolStatus
	arc    zfs.ARCLimits
	conf   []byte // nil: no /etc/modprobe.d/zfs.conf
}

func (f *fakeReader) Identify(_ context.Context, names []string) (map[string]zfs.Identity, error) {
	out := map[string]zfs.Identity{}
	for _, n := range names {
		if id, ok := f.ids[n]; ok {
			out[n] = id
		}
	}
	return out, nil
}

func (f *fakeReader) Descendants(_ context.Context, ds string) ([]string, error) {
	return f.kids[ds], nil
}

func (f *fakeReader) Get(_ context.Context, names, props []string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	for _, n := range names {
		out[n] = map[string]string{}
		for _, p := range props {
			if v, ok := f.props[n][p]; ok {
				out[n][p] = v
			}
		}
	}
	return out, nil
}

func (f *fakeReader) Holds(_ context.Context, snaps []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, s := range snaps {
		out[s] = f.holds[s]
	}
	return out, nil
}

func (f *fakeReader) DestroyDryRun(_ context.Context, opts []string, target string) (zfs.DestroyPreview, error) {
	k := strings.Join(append(opts, target), " ")
	return f.dry[k], f.dryErr[k]
}

func (f *fakeReader) PointsOf(_ context.Context, ds string) ([]zfs.Point, error) {
	return f.points[ds], nil
}

func (f *fakeReader) PoolActivity(_ context.Context, pool string) (zfs.Activity, error) {
	return f.pools[pool], nil
}

func (f *fakeReader) PoolTopology(_ context.Context, pool string) (zfs.PoolStatus, error) {
	return f.topo[pool], nil
}

func (f *fakeReader) ARCLimits() (zfs.ARCLimits, error) { return f.arc, nil }

func (f *fakeReader) Modprobe() ([]byte, bool, error) { return f.conf, f.conf != nil, nil }

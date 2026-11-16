// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// fakeReader holds the objects that exist and what the reads return.
type fakeReader struct {
	ids       map[string]zfs.Identity
	kids      map[string][]string
	props     map[string]map[string]string
	holds     map[string][]string
	dry       map[string]zfs.DestroyPreview // by "opts target"
	dryErr    map[string]error
	points    map[string][]zfs.Point
	pools     map[string]zfs.Activity
	topo      map[string]zfs.PoolStatus
	arc       zfs.ARCLimits
	conf      []byte   // nil: no /etc/modprobe.d/zfs.conf
	names     []string // imported pools
	create    error    // what zpool create -n says, nil: it accepts
	feature   string   // every feature's state; empty: enabled
	objects   map[string][]zfs.Dataset
	found     []zfs.Importable  // what zpool import lists
	poolProps map[string]string // more pool properties, as zpool get prints them
	compat    map[string]bool   // what a compatibility value other than off allows
}

func (f *fakeReader) PoolProperties(_ context.Context, pool name.Pool) (zfs.Pool, error) {
	v := f.feature
	if v == "" {
		v = "enabled"
	}
	props := zfs.Properties{"feature@zpool_checkpoint": {Text: termtext.New(v)}, "feature@raidz_expansion": {Text: termtext.New(v)}}
	for k, val := range f.poolProps {
		props[k] = zfs.Property{Text: termtext.New(val)}
	}
	return zfs.Pool{Name: pool, Properties: props}, nil
}

func (f *fakeReader) PoolObjects(_ context.Context, pool name.Pool) ([]zfs.Dataset, error) {
	return f.objects[pool.String()], nil
}

func (f *fakeReader) CreateDryRun(_ context.Context, _, operands []string) (termtext.Text, error) {
	if f.create != nil {
		return termtext.Text{}, f.create
	}
	return termtext.New("would create '" + operands[0] + "' with the following layout:\n"), nil
}

func (f *fakeReader) PoolNames(context.Context) ([]string, error) { return f.names, nil }

func (f *fakeReader) AddDryRun(_ context.Context, _, operands []string) (termtext.Text, error) {
	if f.create != nil {
		return termtext.Text{}, f.create
	}
	return termtext.New("would update '" + operands[0] + "' to the following configuration:\n"), nil
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

func (f *fakeReader) RemoveDryRun(_ context.Context, _, vdev string) (termtext.Text, error) {
	return termtext.New("Memory that will be used after removing " + vdev + ": 1.01K\n"), nil
}

func (f *fakeReader) Importable(context.Context, []string) ([]zfs.Importable, error) {
	return f.found, nil
}

func (f *fakeReader) CompatibleFeatures(value string) (map[string]bool, bool, error) {
	if value == "" || value == "off" {
		return nil, false, nil
	}
	return f.compat, true, nil
}

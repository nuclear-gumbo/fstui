// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/storage"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

const volumeSize = 64 << 20

func runner(t *testing.T) *execute.Runner {
	t.Helper()
	tools, err := execute.Resolve(execute.ZFS, execute.Zpool)
	if err != nil {
		t.Fatal(err)
	}
	return execute.NewRunner(tools)
}

func TestIntegrationDetectVersion(t *testing.T) {
	v, err := DetectVersion(t.Context(), runner(t))
	if err != nil {
		t.Fatal(err)
	}
	if !v.UserlandRelease.AtLeast(Minimum) || v.Userland == "" || v.Kernel == "" {
		t.Fatalf("version = %+v", v)
	}
	_, err = detectVersion(t.Context(), runner(t), filepath.Join(t.TempDir(), "zfs"))
	if !errors.Is(err, ErrModuleNotLoaded) {
		t.Fatalf("missing module directory: err = %v, want ErrModuleNotLoaded", err)
	}
}

func findPool(t *testing.T, r *Reader, name string) storage.Pool {
	t.Helper()
	pools, err := r.Pools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pools {
		if p.Name.Raw() == name {
			return p
		}
	}
	t.Fatalf("pool %s not in Pools()", name)
	return storage.Pool{}
}

func status(t *testing.T, r *Reader, p storage.Pool) storage.PoolStatus {
	t.Helper()
	st, err := r.PoolStatus(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// fixture is one test pool with a filesystem, a volume and snapshots,
// which the steps of TestIntegrationReaders inspect and then change.
type fixture struct {
	tp            *testpool.Pool
	r             *Reader
	pool          storage.Pool
	home, vol     string
	first, second string
}

func TestIntegrationReaders(t *testing.T) {
	tp := testpool.New(t, "mirror", 2)
	f := &fixture{
		tp:     tp,
		r:      NewReader(runner(t)),
		home:   tp.CreateFilesystem("home"),
		vol:    tp.CreateVolume("vol", volumeSize),
		first:  tp.Snapshot("home", "first"),
		second: tp.Snapshot("home", "second"),
	}
	tp.Snapshot("", "root")
	f.pool = findPool(t, f.r, tp.Name)

	// Steps share the pool and run in order; later ones change its state.
	t.Run("pool", f.checkPool)
	t.Run("overview", f.checkOverview)
	t.Run("status", f.checkStatus)
	t.Run("properties", f.checkProperties)
	t.Run("filesystems", f.checkFilesystems)
	t.Run("volumes", f.checkVolumes)
	t.Run("snapshots", f.checkSnapshots)
	t.Run("datasets", f.checkDatasets)
	t.Run("all snapshots and volumes", f.checkAllLists)
	t.Run("pool datasets", f.checkPoolDatasets)
	t.Run("degraded", f.checkDegraded)
	t.Run("scrubbed", f.checkScrubbed)
}

func (f *fixture) checkPool(t *testing.T) {
	if p := f.pool; !p.Health.OK() || p.Size == 0 || p.Allocated+p.Free != p.Size {
		t.Fatalf("new pool = %+v", p)
	}
}

func (f *fixture) checkOverview(t *testing.T) {
	all, err := f.r.Overviews(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range all {
		if o.Pool.Name.String() != f.tp.Name {
			continue
		}
		// A fresh pool with a little data: some bytes used, most available,
		// and together no more than the raw size.
		if o.Used == 0 || o.Available == 0 || o.Used+o.Available > o.Pool.Properties["size"].Number {
			t.Fatalf("root dataset used %d, available %d, pool size %d", o.Used, o.Available, o.Pool.Properties["size"].Number)
		}
		if o.Status.State != "ONLINE" || o.Status.Data.Children[0].Type != "mirror" {
			t.Fatalf("status = %s with %s", o.Status.State, o.Status.Data.Children[0].Type)
		}
		return
	}
	t.Fatalf("pool %s not in Overviews()", f.tp.Name)
}

func (f *fixture) checkProperties(t *testing.T) {
	pn, err := name.ParsePool(f.tp.Name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.r.PoolProperties(t.Context(), pn)
	if err != nil {
		t.Fatal(err)
	}
	// testpool creates pools with cachefile=none: a local, non-default value.
	if c := p.Properties["cachefile"]; c.Text.Raw() != "none" || c.Source.Kind != SourceLocal {
		t.Errorf("cachefile = %q from %s, want none, LOCAL", c.Text.Raw(), c.Source.Word())
	}
	if fe := p.Properties["feature@zpool_checkpoint"]; fe.Text.Raw() != "enabled" {
		t.Errorf("feature@zpool_checkpoint = %q, want enabled on a new pool", fe.Text.Raw())
	}
}

func (f *fixture) checkStatus(t *testing.T) {
	st := status(t, f.r, f.pool)
	if len(st.Devices) != 1 || st.Devices[0].Kind != "mirror" || st.Devices[0].Role != storage.Data {
		t.Fatalf("devices = %+v, want one data mirror", st.Devices)
	}
	for i, d := range st.Devices[0].Children {
		if d.Kind != "file" || d.Name.Raw() != f.tp.Images[i] || !d.Health.OK() {
			t.Errorf("mirror member %d = %s %s %s, want file %s ONLINE", i, d.Kind, d.Name.Raw(), d.Health.Word, f.tp.Images[i])
		}
	}
	if st.Scan != nil {
		t.Errorf("new pool has scan %+v", st.Scan)
	}
}

func (f *fixture) filesystems(t *testing.T) []storage.Filesystem {
	t.Helper()
	fss, err := f.r.Filesystems(t.Context(), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(fss) != 2 || fss[0].Name.Raw() != f.tp.Name || fss[1].Name.Raw() != f.home {
		t.Fatalf("filesystems = %v, want %s then %s (no volume, no snapshots)", names(fss), f.tp.Name, f.home)
	}
	return fss
}

func (f *fixture) checkFilesystems(t *testing.T) {
	home := f.filesystems(t)[1]
	if want := filepath.Join(testpool.Dir, "mnt", f.home); home.Mountpoint.Raw() != want {
		t.Errorf("mountpoint = %s, want %s", home.Mountpoint.Raw(), want)
	}
}

func (f *fixture) checkVolumes(t *testing.T) {
	vols, err := f.r.Volumes(t.Context(), f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 1 || vols[0].Name.Raw() != f.vol || vols[0].Size != volumeSize {
		t.Fatalf("volumes = %+v, want %s of %d bytes", vols, f.vol, volumeSize)
	}
}

func (f *fixture) checkSnapshots(t *testing.T) {
	snaps, err := f.r.Snapshots(t.Context(), f.filesystems(t)[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 || snaps[0].Name.Raw() != f.first || snaps[1].Name.Raw() != f.second {
		t.Fatalf("snapshots of home = %v, want %s then %s only", snapNames(snaps), f.first, f.second)
	}
	if snaps[0].Created.IsZero() || snaps[1].Created.Before(snaps[0].Created) {
		t.Errorf("creation times %v, %v", snaps[0].Created, snaps[1].Created)
	}
}

func (f *fixture) checkDatasets(t *testing.T) {
	all, err := f.r.Datasets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	mine := map[string]Dataset{}
	for _, d := range all {
		if d.Pool.String() == f.tp.Name {
			mine[d.Name] = d
		}
	}
	if len(mine) != 3 || mine[f.vol].Type != Volume || mine[f.home].Type != Filesystem {
		t.Fatalf("datasets of the pool = %v, want root, home and the volume without snapshots", mine)
	}
	// Unencrypted datasets report keystatus as not applicable.
	if ks := mine[f.home].Properties["keystatus"]; ks.Text.Raw() != "-" {
		t.Errorf("keystatus = %q", ks.Text.Raw())
	}
	for _, d := range mine {
		checkUsedBy(t, d)
	}

	home, err := name.ParseDataset(f.home)
	if err != nil {
		t.Fatal(err)
	}
	props, err := f.r.DatasetProperties(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	if mp := props.Properties["mountpoint"]; mp.Source.Kind != SourceInherited || mp.Source.From().Raw() != f.tp.Name {
		t.Errorf("mountpoint source = %s from %q, want inherited from the pool", mp.Source.Word(), mp.Source.From().Raw())
	}
	snaps, err := f.r.DatasetSnapshots(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 || snaps[0].Name != f.first {
		t.Fatalf("snapshots = %v", snaps)
	}
	if n, ok := snaps[0].Properties.Number("userrefs"); !ok || n != 0 {
		t.Errorf("userrefs = %d (%v), want 0 holds", n, ok)
	}
}

func (f *fixture) checkAllLists(t *testing.T) {
	snaps, err := f.r.AllSnapshots(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var mine []string
	for _, s := range snaps {
		if s.Pool.String() == f.tp.Name {
			mine = append(mine, s.Name)
		}
	}
	// Taken in this order: home@first, home@second, then the root's.
	if want := []string{f.first, f.second, f.tp.Name + "@root"}; strings.Join(mine, ",") != strings.Join(want, ",") {
		t.Errorf("snapshots = %v, want %v in creation order", mine, want)
	}
	vols, err := f.r.AllVolumes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vols {
		if v.Name != f.vol {
			continue
		}
		size, _ := v.Properties.Number("volsize")
		resv, _ := v.Properties.Number("refreservation")
		// testpool creates the volume sparse (zfs create -s): no refreservation.
		if size != volumeSize || resv != 0 {
			t.Errorf("volume size %d, refreservation %d; want %d sparse", size, resv, volumeSize)
		}
		return
	}
	t.Errorf("volume %s not in AllVolumes", f.vol)
}

func (f *fixture) checkPoolDatasets(t *testing.T) {
	pn, err := name.ParsePool(f.tp.Name)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := f.r.PoolDatasets(t.Context(), pn)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range ds {
		got = append(got, d.Name)
		checkUsedBy(t, d)
	}
	if want := []string{f.tp.Name, f.home, f.vol}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pool datasets = %v, want %v", got, want)
	}
}

// checkUsedBy asserts zfsprops(7): used = usedbychildren + usedbydataset
// + usedbyrefreservation + usedbysnapshots.
func checkUsedBy(t *testing.T, d Dataset) {
	t.Helper()
	var parts uint64
	for _, prop := range []string{"usedbychildren", "usedbydataset", "usedbyrefreservation", "usedbysnapshots"} {
		n, ok := d.Properties.Number(prop)
		if !ok {
			t.Fatalf("%s: %s missing", d.Name, prop)
		}
		parts += n
	}
	if used, _ := d.Properties.Number("used"); used != parts {
		t.Errorf("%s: used %d, usedby* sum %d", d.Name, used, parts)
	}
}

func (f *fixture) checkDegraded(t *testing.T) {
	f.tp.Offline(1)
	if p := findPool(t, f.r, f.tp.Name); p.Health.Condition != storage.Degraded {
		t.Fatalf("pool with an offline mirror member = %s, want DEGRADED", p.Health.Word)
	}
	st := status(t, f.r, f.pool)
	if m := st.Devices[0].Children[1]; m.Health.Condition != storage.Offline || st.Health.Condition != storage.Degraded {
		t.Fatalf("after offline: pool %s, member %s", st.Health.Word, m.Health.Word)
	}
	if len(st.Problem) == 0 || len(st.Action) == 0 {
		t.Errorf("degraded pool has no explanation: problem %v, action %v", st.Problem, st.Action)
	}
}

func (f *fixture) checkScrubbed(t *testing.T) {
	f.tp.Scrub()
	sc := status(t, f.r, f.pool).Scan
	if sc == nil || sc.Word != "SCRUB" || sc.State != storage.Finished || sc.End.IsZero() || sc.Errors != 0 {
		t.Fatalf("after scrub: scan = %+v, want a finished scrub without errors", sc)
	}
}

func names(fss []storage.Filesystem) []string {
	out := make([]string, len(fss))
	for i, f := range fss {
		out[i] = f.Name.Raw()
	}
	return out
}

func snapNames(snaps []storage.Snapshot) []string {
	out := make([]string, len(snaps))
	for i, s := range snaps {
		out[i] = s.Name.Raw()
	}
	return out
}

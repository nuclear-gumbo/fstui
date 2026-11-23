// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/statedir"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Remote hosts are tested against this machine itself: FSTUI_TEST_SSH_HOST
// names an ssh alias that reaches root here (CONTRIBUTING.md, Tests), and
// FSTUI_TEST_SSH_BADKEY one whose known host key is wrong. Every read over
// ssh must equal the same read made locally.
const controlDir = "/run/fstui-test-ssh"

func remoteTo(t *testing.T, env string) *execute.Remote {
	t.Helper()
	return remoteVia(t, env, controlDir)
}

// remoteVia is remoteTo with its own socket directory: ssh names a
// shared connection by host, port and user (%C), so two aliases of
// 127.0.0.1 would share one, and the second's host key check would
// never run.
func remoteVia(t *testing.T, env, dir string) *execute.Remote {
	t.Helper()
	dest := os.Getenv(env)
	if dest == "" {
		t.Skipf("%s is not set", env)
	}
	if err := statedir.Private(dir); err != nil {
		t.Fatal(err)
	}
	tools, err := execute.Resolve(execute.SSH)
	if err != nil {
		t.Fatal(err)
	}
	r := execute.NewRemote(execute.NewRunner(tools), execute.Host{
		Destination: dest, ControlDir: dir,
		ConnectTimeout: 10 * time.Second, AliveInterval: 15 * time.Second, Persist: 30 * time.Second,
	})
	return r
}

// localAndRemote are readers of the same machine, one through ssh, and
// the probed connection.
func localAndRemote(t *testing.T) (*zfs.Reader, *zfs.Reader, *execute.Remote) {
	t.Helper()
	rem := remoteTo(t, "FSTUI_TEST_SSH_HOST")
	p, err := rem.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Require(execute.RemoteEnv, execute.ZFS, execute.Zpool); err != nil || p.UID != 0 {
		t.Fatalf("probe: uid %d, %v", p.UID, err)
	}
	tools, err := execute.ResolveOptional([]execute.Tool{execute.ZFS, execute.Zpool}, []execute.Tool{execute.Systemctl})
	if err != nil {
		t.Fatal(err)
	}
	remote := zfs.NewReader(rem)
	remote.ReadFile, remote.ReadDir = rem.ReadFile, rem.ReadDir
	return zfs.NewReader(execute.NewRunner(tools)), remote, rem
}

func TestIntegrationRemoteReads(t *testing.T) {
	local, remote, rem := localAndRemote(t)
	pool := testpool.New(t, "mirror", 2)
	pool.CreateFilesystem("data")
	pool.Snapshot("data", "first")
	spaced := pool.CreateFilesystem("with space:and colon")
	ctx := context.Background()

	// A name the remote shell would split or read is passed as one word.
	ds, err := name.ParseDataset(spaced)
	if err != nil {
		t.Fatal(err)
	}
	lp, err1 := local.DatasetProperties(ctx, ds)
	rp, err2 := remote.DatasetProperties(ctx, ds)
	if err1 != nil || err2 != nil || lp.Name != rp.Name || len(lp.Properties) != len(rp.Properties) {
		t.Errorf("%q: local %v, remote %v", spaced, err1, err2)
	}

	lo, err1 := local.Overviews(ctx)
	ro, err2 := remote.Overviews(ctx)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	find := func(os []zfs.Overview) zfs.Overview {
		i := slices.IndexFunc(os, func(o zfs.Overview) bool { return o.Pool.Name.String() == pool.Name })
		if i < 0 {
			t.Fatalf("%s not listed", pool.Name)
		}
		return os[i]
	}
	l, r := find(lo), find(ro)
	if l.Status.State != r.Status.State || l.Used != r.Used || l.Available != r.Available || len(l.Status.Data.Children) != len(r.Status.Data.Children) {
		t.Errorf("overview: local %s %d/%d %d devices, remote %s %d/%d %d", l.Status.State, l.Used, l.Available, len(l.Status.Data.Children),
			r.Status.State, r.Used, r.Available, len(r.Status.Data.Children))
	}

	ls, err1 := local.AllSnapshots(ctx)
	rs, err2 := remote.AllSnapshots(ctx)
	if err1 != nil || err2 != nil || len(ls) != len(rs) {
		t.Errorf("snapshots: %d %v, remote %d %v", len(ls), err1, len(rs), err2)
	}
	lv, err1 := zfs.DetectVersion(ctx, execute.NewRunner(mustResolve(t, execute.ZFS)))
	rv, err2 := zfs.DetectVersionOn(ctx, rem)
	if err1 != nil || err2 != nil || lv != rv {
		t.Errorf("version: local %+v %v, remote %+v %v", lv, err1, rv, err2)
	}

	lsch, err1 := local.ScrubSchedules(ctx, lo)
	rsch, err2 := remote.ScrubSchedules(ctx, ro)
	if err1 != nil || err2 != nil || len(lsch) != len(rsch) {
		t.Errorf("scrub schedules: %v %v, remote %v %v", lsch, err1, rsch, err2)
	}

	pn, _ := name.ParsePool(pool.Name)
	lt, err1 := local.ReadTuneSample(pn, time.Now())
	rt, err2 := remote.ReadTuneSample(pn, time.Now())
	if err1 != nil || err2 != nil || len(lt.Objsets) != len(rt.Objsets) || len(rt.Objsets) == 0 {
		t.Errorf("tune: %d objsets %v, remote %d %v", len(lt.Objsets), err1, len(rt.Objsets), err2)
	}
	if _, err := remote.ARCStats(); err != nil {
		t.Errorf("ARC over ssh: %v", err)
	}
	if _, _, err := remote.Events(ctx); err != nil {
		t.Errorf("events over ssh: %v", err)
	}

	sctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	n := 0
	_ = remote.TuneIO(sctx, pn, 2*time.Second, func(zfs.IOPool) { n++ })
	if n == 0 {
		t.Error("zpool iostat over ssh gave no interval in 8s")
	}
}

func mustResolve(t *testing.T, tools ...execute.Tool) *execute.Tools {
	t.Helper()
	r, err := execute.Resolve(tools...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Nothing runs before the probe; a shared connection whose socket is
// gone, as after its master was killed, is replaced by the next command.
func TestIntegrationRemoteReconnects(t *testing.T) {
	rem := remoteTo(t, "FSTUI_TEST_SSH_HOST")
	if _, err := zfs.DetectVersionOn(context.Background(), rem); !errors.Is(err, execute.ErrNotResolved) {
		t.Errorf("version before the probe: %v", err)
	}
	if _, err := rem.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	sockets, err := filepath.Glob(filepath.Join(controlDir, "*"))
	if err != nil || len(sockets) == 0 {
		t.Fatalf("no shared connection after the probe: %v %v", sockets, err)
	}
	for _, s := range sockets {
		if err := os.Remove(s); err != nil {
			t.Fatal(err)
		}
	}
	r := zfs.NewReader(rem)
	if _, err := r.PoolNames(context.Background()); err != nil {
		t.Errorf("after the connection closed: %v", err)
	}
}

// A changed host key and a host that does not resolve are reported as
// unreachable, never as a command's result.
func TestIntegrationRemoteRefused(t *testing.T) {
	for _, env := range []string{"FSTUI_TEST_SSH_BADKEY", ""} {
		var rem *execute.Remote
		if env == "" {
			t.Setenv("FSTUI_TEST_SSH_NOHOST", "fstuitest-no-such-host.invalid")
			rem = remoteTo(t, "FSTUI_TEST_SSH_NOHOST")
		} else {
			rem = remoteVia(t, env, controlDir+"-badkey")
		}
		if _, err := rem.Probe(context.Background()); !errors.Is(err, execute.ErrUnreachable) {
			t.Errorf("%s: probe err = %v, want unreachable", rem.Destination(), err)
		}
	}
}

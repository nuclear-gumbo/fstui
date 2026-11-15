// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration && !fstui_nowrite

package zfs_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

func TestIntegrationDatasetLifecycle(t *testing.T) {
	p := testpool.New(t, "", 1)
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	ds := p.Name + "/a"
	mp := filepath.Join(testpool.Dir, "mnt", p.Name+"-a")

	c, err := b.DatasetCreate(ctx, ds, map[string]string{"compression": "zstd", "quota": "64M", "mountpoint": mp})
	mustRun(t, pl, c, err)
	got, err := b.R.Get(ctx, []string{ds}, []string{"compression", "quota", "mountpoint", "mounted"})
	if err != nil || got[ds]["compression"] != "zstd" || got[ds]["quota"] != "67108864" || got[ds]["mountpoint"] != mp || got[ds]["mounted"] != "yes" {
		t.Fatalf("after create: %v, %v", got, err)
	}

	s, err := b.PropertySet(ctx, ds, "atime", "off")
	mustRun(t, pl, s, err)
	in, err := b.PropertyInherit(ctx, ds, "atime")
	mustRun(t, pl, in, err)

	u, err := b.Unmount(ctx, ds, false)
	mustRun(t, pl, u, err)
	m, err := b.Mount(ctx, ds)
	mustRun(t, pl, m, err)

	to := p.Name + "/b"
	r, err := b.DatasetRename(ctx, ds, to)
	mustRun(t, pl, r, err)
	p.CreateFilesystem("b/child")

	d, err := b.DatasetDestroy(ctx, to, false)
	if err != nil || len(d.Blocked) == 0 || !strings.Contains(d.Blocked[0], "children") {
		t.Fatalf("destroy with children, no -r: %q, %v", d.Blocked, err)
	}
	d, err = b.DatasetDestroy(ctx, to, true)
	if err != nil || !slices.ContainsFunc(d.Preview.Destroys, func(x termtext.Text) bool { return x.Raw() == to+"/child" }) {
		t.Fatalf("destroy -r preview: %+v, %v", d.Preview, err)
	}
	if mutate.TierOf(d) != mutate.High {
		t.Fatalf("tier %s", mutate.TierOf(d))
	}
	mustRun(t, pl, d, err)
	ids, err := b.R.Identify(ctx, []string{to, to + "/child"})
	if err != nil || len(ids) != 0 {
		t.Fatalf("after destroy: %v, %v", ids, err)
	}
}

func TestIntegrationPropertySetRefusesAfterAnotherChange(t *testing.T) {
	p := testpool.New(t, "", 1)
	ds := p.CreateFilesystem("a")
	b, pl, _, _ := writeSetup(t)
	s, err := b.PropertySet(t.Context(), ds, "compression", "zstd")
	if err != nil {
		t.Fatal(err)
	}
	p.SetProperty("a", "compression", "gzip")
	if _, err := pl.Run(t.Context(), s); err == nil || !strings.Contains(err.Error(), "compression") {
		t.Fatalf("Run after another change = %v", err)
	}
}

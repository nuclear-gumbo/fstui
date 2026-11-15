// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration && !fstui_nowrite

package zfs_test

import (
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

func TestIntegrationVolumeCreateResize(t *testing.T) {
	p := testpool.New(t, "", 1)
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	vol := p.Name + "/vm"
	cr, err := b.VolumeCreate(ctx, vol, "64M", true, "16K")
	mustRun(t, pl, cr, err)
	grow, err := b.VolumeResize(ctx, vol, "128M")
	if err != nil || mutate.TierOf(grow) != mutate.Medium {
		t.Fatalf("grow: %+v, %v", grow, err)
	}
	mustRun(t, pl, grow, err)
	odd, err := b.VolumeResize(ctx, vol, "100000")
	if err != nil || len(odd.Blocked) == 0 {
		t.Fatalf("size not a multiple of the block size: %q, %v", odd.Blocked, err)
	}
	shrink, err := b.VolumeResize(ctx, vol, "32M")
	if err != nil || mutate.TierOf(shrink) != mutate.High {
		t.Fatalf("shrink: %+v, %v", shrink, err)
	}
	mustRun(t, pl, shrink, err)
	got, err := b.R.Get(ctx, []string{vol}, []string{"volsize", "volblocksize", "refreservation"})
	if err != nil || got[vol]["volsize"] != "33554432" || got[vol]["volblocksize"] != "16384" || got[vol]["refreservation"] != "0" {
		t.Fatalf("after resizing: %v, %v", got, err)
	}
}

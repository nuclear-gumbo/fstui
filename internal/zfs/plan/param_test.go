// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
)

func TestParamSet(t *testing.T) {
	f := &fakeReader{params: map[string]uint64{"zfs_prefetch_disable": 0}}
	p, err := Builder{R: f}.ParamSet(t.Context(), "zfs_prefetch_disable", "1")
	if err != nil || len(p.Blocked) != 0 || mutate.TierOf(p) != mutate.Medium {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if got := p.Steps[0].String(); got != "tee -- /sys/module/zfs/parameters/zfs_prefetch_disable" || string(p.Steps[0].Input) != "1\n" {
		t.Errorf("step %s %q", got, p.Steps[0].Input)
	}
	f.params["zfs_prefetch_disable"] = 1
	p, _ = Builder{R: f}.ParamSet(t.Context(), "zfs_prefetch_disable", "1")
	if !strings.Contains(strings.Join(p.Blocked, ";"), "zfs_prefetch_disable is already 1") {
		t.Errorf("unchanged: %v", p.Blocked)
	}
	f.params["zfs_arc_meta_balance"] = 5000
	p, _ = Builder{R: f}.ParamSet(t.Context(), "zfs_arc_meta_balance", "2000")
	if !strings.Contains(strings.Join(p.Blocked, ";"), "zfs_arc_meta_balance is already 5000, more than 2000") {
		t.Errorf("lowering: %v", p.Blocked)
	}
	for _, c := range [][2]string{{"zfs_arc_max", "1"}, {"zfs_prefetch_disable", "2"}, {"zfs_arc_meta_balance", "0"}, {"zfs_arc_meta_balance", "x"}} {
		if _, err := (Builder{R: f}).ParamSet(t.Context(), c[0], c[1]); err == nil {
			t.Errorf("%s=%s accepted", c[0], c[1])
		}
	}
}

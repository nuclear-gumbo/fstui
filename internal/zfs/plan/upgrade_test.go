// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
)

func TestPoolUpgrade(t *testing.T) {
	f := mirrorPool("mirror")
	f.poolProps = map[string]string{"feature@block_cloning": "disabled", "feature@fast_dedup": "disabled", "feature@lz4_compress": "active", "compatibility": "off", "bootfs": "tank/root"}
	p, err := Builder{R: f}.PoolUpgrade(t.Context(), "tank")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool upgrade -- tank" || mutate.TierOf(p) != mutate.High {
		t.Fatalf("plan %+v, %v", p, err)
	}
	var aff []string
	for _, a := range p.Preview.Affects {
		aff = append(aff, a.Raw())
	}
	all := strings.Join(aff, "\n")
	if !strings.Contains(all, "2 features are enabled: block_cloning, fast_dedup") || !strings.Contains(all, "stop the machine booting") {
		t.Errorf("affects:\n%s", all)
	}

	// grub2 allows block_cloning only; fast_dedup stays disabled.
	f.poolProps["compatibility"], f.compat = "grub2", map[string]bool{"block_cloning": true}
	p, _ = Builder{R: f}.PoolUpgrade(t.Context(), "tank")
	all = ""
	for _, a := range p.Preview.Affects {
		all += a.Raw() + "\n"
	}
	if len(p.Blocked) != 0 || !strings.Contains(all, "1 feature is enabled: block_cloning") || !strings.Contains(all, "keeps 1 disabled: fast_dedup") || strings.Contains(all, "booting") {
		t.Errorf("grub2: blocked %v, affects:\n%s", p.Blocked, all)
	}

	f.compat = map[string]bool{}
	p, _ = Builder{R: f}.PoolUpgrade(t.Context(), "tank")
	if !strings.Contains(blocked(p), "of tank (grub2) allows none of its 2 disabled features") {
		t.Errorf("nothing allowed: %q", blocked(p))
	}
	f.poolProps = map[string]string{"feature@lz4_compress": "active"}
	p, _ = Builder{R: f}.PoolUpgrade(t.Context(), "tank")
	if !strings.Contains(blocked(p), "enabled on tank already") {
		t.Errorf("up to date: %q", blocked(p))
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// PoolUpgrade plans zpool upgrade: it enables every disabled feature the
// pool's compatibility property allows. zpool upgrade without a pool
// lists features compatibility would hold back, so fstui reads the
// compatibility files itself to say what is enabled.
func (b Builder) PoolUpgrade(ctx context.Context, pool string) (*mutate.Plan, error) {
	pn, err := name.ParsePool(pool)
	if err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	props, err := b.R.PoolProperties(ctx, pn)
	if err != nil {
		return nil, err
	}
	compat := props.Properties["compatibility"].Text.Raw()
	allowed, limited, err := b.R.CompatibleFeatures(compat)
	if err != nil {
		return nil, err
	}
	enable, held := disabledFeatures(props.Properties, allowed, limited)
	p := newPlan(mutate.PoolUpgrade, "Upgrade "+show(pool), []string{pool}, map[string]string{"pool": pool},
		execute.Argv{Tool: execute.Zpool, Options: []string{"upgrade"}, Operands: []string{pool}})
	blockIf(p, b.Unprivileged, "upgrading a pool needs root")
	switch {
	case len(enable) == 0 && len(held) > 0:
		blockIf(p, true, fmt.Sprintf("the compatibility setting of %s (%s) allows none of its %d disabled features", show(pool), show(compat), len(held)))
	case len(enable) == 0:
		blockIf(p, true, "every feature this system supports is enabled on "+show(pool)+" already")
	}
	if len(enable) > 0 {
		affects(p, fmt.Sprintf("%d %s enabled: %s", len(enable), plural(len(enable), "feature is", "features are"), strings.Join(enable, ", ")),
			"OpenZFS versions without them (older systems, rescue media) can no longer import "+show(pool)+" once a feature is in use")
	}
	if len(held) > 0 {
		affects(p, fmt.Sprintf("the compatibility setting of %s (%s) keeps %d disabled: %s", show(pool), show(compat), len(held), strings.Join(held, ", ")))
	}
	if boot := props.Properties["bootfs"].Text.Raw(); boot != "" && boot != "-" && !limited {
		affects(p, show(pool)+" boots this system ("+show(boot)+") and has no compatibility setting: if a boot loader such as GRUB reads it, a feature it lacks can stop the machine booting; set compatibility (for example grub2) first")
	}
	p.Irreversible = []string{"An enabled feature cannot be disabled again."}
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		props, err := b.R.PoolProperties(ctx, pn)
		if err != nil {
			return nil, err
		}
		f := mutate.Fingerprint{pool + " (compatibility)": props.Properties["compatibility"].Text.Raw()}
		for _, feat := range enable {
			f[pool+" feature@"+feat] = props.Properties["feature@"+feat].Text.Raw()
		}
		return f, nil
	}
	if p.Baseline, err = read(ctx); err != nil {
		return nil, err
	}
	p.Recheck = read
	return p, nil
}

// disabledFeatures splits the pool's disabled features into those zpool
// upgrade enables and those its compatibility setting holds back.
func disabledFeatures(props zfs.Properties, allowed map[string]bool, limited bool) (enable, held []string) {
	for k, v := range props {
		f, ok := strings.CutPrefix(k, "feature@")
		switch {
		case !ok || v.Text.Raw() != "disabled":
		case limited && !allowed[f]:
			held = append(held, f)
		default:
			enable = append(enable, f)
		}
	}
	slices.Sort(enable)
	slices.Sort(held)
	return enable, held
}

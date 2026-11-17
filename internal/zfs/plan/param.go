// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"strconv"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
)

// tunables are the module parameters tune mode can point at, each with
// the values it may be set to and what that does.
var tunables = map[string]struct {
	min, max uint64
	does     string
}{
	"zfs_prefetch_disable": {0, 1, "1 stops ZFS reading ahead of sequential reads; 0 lets it"},
	"zfs_arc_meta_balance": {1, 100000, "higher values keep metadata in the ARC in favour of data (500 by default)"},
}

// ParamSet plans raising one of tunables now, until the next reboot or
// reload of the zfs module. Tune mode only ever points upward, so a value
// below the current one is refused rather than lowering it by surprise.
func (b Builder) ParamSet(_ context.Context, param, value string) (*mutate.Plan, error) {
	t, ok := tunables[param]
	if !ok {
		return nil, fmt.Errorf("%s is not a parameter fstui sets", show(param))
	}
	v, err := strconv.ParseUint(value, 10, 64)
	if err != nil || v < t.min || v > t.max {
		return nil, fmt.Errorf("%s takes %d to %d, not %s", param, t.min, t.max, show(value))
	}
	cur, err := b.R.ModuleParam(param)
	if err != nil {
		return nil, err
	}
	p := newPlan(mutate.ParamSet, "Set "+param+" to "+value, []string{param}, map[string]string{"param": param, "value": value}, paramArgv(param, v))
	blockIf(p, b.Unprivileged, "module parameters need root")
	blockIf(p, cur == v, param+" is already "+value)
	blockIf(p, cur > v, param+" is already "+strconv.FormatUint(cur, 10)+", more than "+value)
	affects(p, param+": "+strconv.FormatUint(cur, 10)+" → "+value+"; "+t.does,
		"this lasts until the next reboot or reload of the zfs module")
	p.Baseline = mutate.Fingerprint{param: strconv.FormatUint(cur, 10)}
	p.Recheck = func(context.Context) (mutate.Fingerprint, error) {
		n, err := b.R.ModuleParam(param)
		return mutate.Fingerprint{param: strconv.FormatUint(n, 10)}, err
	}
	return p, nil
}

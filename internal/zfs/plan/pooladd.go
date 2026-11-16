// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// AddSpec is what to add to an existing pool: a data vdev of Layout
// from Data, and log, cache, special and spare devices.
type AddSpec struct {
	Pool       string
	Layout     string
	Data       []string
	Log        []string
	Cache      []string
	Special    []string
	Spare      []string
	Ashift     string
	Overwrite  bool // destroy signatures found on the devices
	Checkpoint bool // take a checkpoint first, to rewind to if the result is wrong
}

func (s AddSpec) params() map[string]string {
	return map[string]string{
		"pool": s.Pool, "layout": s.Layout, "data": joinList(s.Data), "log": joinList(s.Log), "cache": joinList(s.Cache),
		"special": joinList(s.Special), "spare": joinList(s.Spare), "ashift": s.Ashift,
		"overwrite": boolParam(s.Overwrite), "checkpoint": boolParam(s.Checkpoint),
	}
}

func addSpecFromParams(p map[string]string) AddSpec {
	return AddSpec{
		Pool: p["pool"], Layout: p["layout"], Data: splitList(p["data"]), Log: splitList(p["log"]),
		Cache: splitList(p["cache"]), Special: splitList(p["special"]), Spare: splitList(p["spare"]),
		Ashift: p["ashift"], Overwrite: p["overwrite"] == "true", Checkpoint: p["checkpoint"] == "true",
	}
}

// vdevs is the spec as PoolSpec carries it, for the parts both share.
func (s AddSpec) vdevs() PoolSpec {
	return PoolSpec{Name: s.Pool, Layout: s.Layout, Data: s.Data, Log: s.Log, Cache: s.Cache, Special: s.Special, Spare: s.Spare, Ashift: s.Ashift}
}

// PoolAdd plans zpool add. The new data vdev must have the redundancy the
// pool's data vdevs have: zpool would accept a mismatch with -f, which
// overwrite also needs, so fstui checks it itself and never forces it.
func (b Builder) PoolAdd(ctx context.Context, s AddSpec) (*mutate.Plan, error) {
	if b.Devices == nil {
		return nil, ErrNoInventory
	}
	pn, err := name.ParsePool(s.Pool)
	if err != nil {
		return nil, err
	}
	if _, ok := minData[s.Layout]; !ok {
		return nil, fmt.Errorf("layout %s is not stripe, mirror, raidz1, raidz2 or raidz3", show(s.Layout))
	}
	v := s.vdevs()
	opts, err := v.options()
	if err != nil {
		return nil, err
	}
	inv, err := b.Devices(ctx)
	if err != nil {
		return nil, err
	}
	st, err := b.R.PoolTopology(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	operands := v.operands()
	p := newPlan(mutate.PoolAdd, "Add devices to "+show(s.Pool), []string{s.Pool}, s.params())
	blockIf(p, b.Unprivileged, "adding devices needs root")
	blockIf(p, len(v.all()) == 0, "choose at least one device to add")
	blockIf(p, len(s.Data) > 0 && len(s.Data) < minData[s.Layout], fmt.Sprintf("%s needs at least %d devices", layoutWord(s.Layout), minData[s.Layout]))
	blockIf(p, len(s.Special) == 1, "a single special device holds metadata for the whole pool: losing it loses the pool; choose two, as a mirror")
	if have := DataLayouts(st); len(s.Data) > 0 && len(have) > 0 && !slices.Contains(have, s.Layout) {
		blockIf(p, true, fmt.Sprintf("the data vdevs of %[3]s are %[1]s; adding %[2]s would give %[3]s two levels of redundancy, and the weaker one decides what it survives",
			strings.Join(wordsFor(have), " and "), layoutWord(s.Layout), show(s.Pool)))
	}
	if err := b.checkpointFirst(ctx, p, s, st, pn); err != nil {
		return nil, err
	}
	if judgeDevices(p, v.all(), s.Overwrite, inv) {
		opts = append([]string{"-f"}, opts...)
	}
	if len(p.Blocked) == 0 {
		out, err := b.R.AddDryRun(ctx, opts, operands)
		if err != nil {
			blockIf(p, true, firstLine(err))
		} else {
			p.Preview.Output = out
		}
	}
	p.Steps = append(p.Steps, execute.Argv{Tool: execute.Zpool, Options: append([]string{"add"}, opts...), Operands: operands})
	if len(s.Data) > 0 {
		affects(p, show(s.Pool)+" grows by "+v.capacity(inv))
	}
	p.Irreversible = addIrreversible(st, s)
	b.watchAdd(p, s, st, inv)
	return p, nil
}

// checkpointFirst, when s asks for it, checks the pool can take a
// checkpoint and makes it the plan's first step.
func (b Builder) checkpointFirst(ctx context.Context, p *mutate.Plan, s AddSpec, st zfs.PoolStatus, pool name.Pool) error {
	if !s.Checkpoint {
		return nil
	}
	blockIf(p, st.Checkpoint != nil, show(s.Pool)+" has a checkpoint already; discard it first, or add without taking one")
	if err := b.checkpointFeature(ctx, p, pool); err != nil {
		return err
	}
	p.Steps = append(p.Steps, execute.Argv{Tool: execute.Zpool, Options: []string{"checkpoint"}, Operands: []string{s.Pool}})
	affects(p, "a checkpoint is taken first: "+show(s.Pool)+" can be rewound to it if the result is wrong; "+checkpointBlocks)
	return nil
}

// DataLayouts are the layouts of the pool's data vdevs, in PoolSpec's
// words: "" for a single device, mirror, raidz1 to raidz3.
func DataLayouts(st zfs.PoolStatus) []string {
	var out []string
	for _, v := range st.Data.Children {
		l := ""
		switch {
		case v.Type == "raidz":
			l = "raidz1"
			for _, n := range []string{"raidz2", "raidz3"} {
				if strings.HasPrefix(v.Name.Raw(), n) {
					l = n
				}
			}
		case len(v.Children) > 0: // mirror, or draid, which fstui does not make
			l = v.Type
		}
		if !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

func wordsFor(layouts []string) []string {
	out := make([]string, len(layouts))
	for i, l := range layouts {
		out[i] = strings.TrimPrefix(layoutWord(l), "a ")
	}
	return out
}

// addIrreversible says which added devices can never leave the pool:
// zpool remove cannot take a top-level vdev out of a pool with raidz.
func addIrreversible(st zfs.PoolStatus, s AddSpec) []string {
	if len(s.Data) == 0 && len(s.Special) == 0 {
		return nil
	}
	raidz := strings.HasPrefix(s.Layout, "raidz")
	for _, l := range DataLayouts(st) {
		raidz = raidz || strings.HasPrefix(l, "raidz")
	}
	if raidz {
		return []string{"The new vdev can never be removed: zpool remove does not work on a pool with raidz vdevs. Only destroying the pool takes it out."}
	}
	return []string{"The new vdev stays until removed with zpool remove, which copies its data to the other vdevs and leaves a mapping behind."}
}

// watchAdd fingerprints the pool's vdevs and checkpoint and the chosen
// devices, and reads them again before running.
func (b Builder) watchAdd(p *mutate.Plan, s AddSpec, st zfs.PoolStatus, inv []disks.Device) {
	facts := func(st zfs.PoolStatus, inv []disks.Device) mutate.Fingerprint {
		f := deviceFacts(s.vdevs().all(), inv)
		f["pool "+s.Pool+" (guid)"] = strconv.FormatUint(st.GUID, 10)
		f["pool "+s.Pool+" (data vdevs)"] = strings.Join(DataLayouts(st), ",") + " x" + strconv.Itoa(len(st.Data.Children))
		f["pool "+s.Pool+" (checkpoint)"] = strconv.FormatBool(st.Checkpoint != nil)
		return f
	}
	p.Baseline = facts(st, inv)
	p.Recheck = func(ctx context.Context) (mutate.Fingerprint, error) {
		inv, err := b.Devices(ctx)
		if err != nil {
			return nil, err
		}
		st, err := b.R.PoolTopology(ctx, s.Pool)
		if err != nil {
			return nil, err
		}
		return facts(st, inv), nil
	}
}

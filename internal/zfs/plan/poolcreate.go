// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// PoolSpec is a new pool as the form describes it. Devices are paths as
// the inventory lists them (/dev/disk/by-id where there is one).
type PoolSpec struct {
	Name        string
	Layout      string // "" (stripe), mirror, raidz1, raidz2, raidz3
	Data        []string
	Log         []string
	Cache       []string
	Special     []string // two or more, as a mirror
	Spare       []string
	Ashift      string // "" lets ZFS choose
	Compression string // "" keeps the default
	Mountpoint  string // "" keeps the default (/<name>)
	Autotrim    bool
	Overwrite   bool // destroy signatures found on the devices (-f)
}

// minData is the fewest data devices each layout takes (zpool create).
var minData = map[string]int{"": 1, "mirror": 2, "raidz1": 2, "raidz2": 3, "raidz3": 4}

// parity is how many devices' worth of each raidz vdev holds parity.
var parity = map[string]int{"raidz1": 1, "raidz2": 2, "raidz3": 3}

// ErrNoInventory is returned where fstui cannot list devices.
var ErrNoInventory = errors.New("creating a pool needs lsblk to list the devices, and fstui did not find it")

func (s PoolSpec) params() map[string]string {
	return map[string]string{
		"pool": s.Name, "layout": s.Layout, "data": joinList(s.Data), "log": joinList(s.Log), "cache": joinList(s.Cache),
		"special": joinList(s.Special), "spare": joinList(s.Spare), "ashift": s.Ashift, "compression": s.Compression,
		"mountpoint": s.Mountpoint, "autotrim": boolParam(s.Autotrim), "overwrite": boolParam(s.Overwrite),
	}
}

func poolSpecFromParams(p map[string]string) PoolSpec {
	return PoolSpec{
		Name: p["pool"], Layout: p["layout"], Data: splitList(p["data"]), Log: splitList(p["log"]),
		Cache: splitList(p["cache"]), Special: splitList(p["special"]), Spare: splitList(p["spare"]),
		Ashift: p["ashift"], Compression: p["compression"], Mountpoint: p["mountpoint"],
		Autotrim: p["autotrim"] == "true", Overwrite: p["overwrite"] == "true",
	}
}

// PoolCreate plans zpool create. Every device must be one the inventory
// lists and Judge allows; one holding a signature needs Overwrite, and is
// then listed as destroyed. zpool create -n checks the layout.
func (b Builder) PoolCreate(ctx context.Context, s PoolSpec) (*mutate.Plan, error) {
	if b.Devices == nil {
		return nil, ErrNoInventory
	}
	if _, err := name.ParsePool(s.Name); err != nil {
		return nil, err
	}
	if _, ok := minData[s.Layout]; !ok {
		return nil, fmt.Errorf("layout %s is not stripe, mirror, raidz1, raidz2 or raidz3", show(s.Layout))
	}
	opts, err := s.options()
	if err != nil {
		return nil, err
	}
	inv, err := b.Devices(ctx)
	if err != nil {
		return nil, err
	}
	pools, err := b.R.PoolNames(ctx)
	if err != nil {
		return nil, err
	}
	operands := s.operands()
	p := newPlan(mutate.PoolCreate, "Create pool "+show(s.Name), []string{s.Name}, s.params(),
		execute.Argv{Tool: execute.Zpool, Options: append([]string{"create"}, opts...), Operands: operands})
	blockIf(p, b.Unprivileged, "creating a pool needs root")
	blockIf(p, slices.Contains(pools, s.Name), "a pool named "+show(s.Name)+" is imported")
	blockIf(p, len(s.Data) < minData[s.Layout], fmt.Sprintf("%s needs at least %d devices", layoutWord(s.Layout), minData[s.Layout]))
	blockIf(p, len(s.Special) == 1, "a single special device holds metadata for the whole pool: losing it loses the pool; choose two, as a mirror")
	if judgeDevices(p, s.all(), s.Overwrite, inv) {
		// -f lets zpool write over the signatures listed as destroyed;
		// it is passed only then, so it overrides no other check.
		opts = append([]string{"-f"}, opts...)
		p.Steps[0].Options = append([]string{"create"}, opts...)
	}
	if len(p.Blocked) == 0 {
		out, err := b.R.CreateDryRun(ctx, opts, operands)
		if err != nil {
			blockIf(p, true, firstLine(err))
		} else {
			p.Preview.Output = out
		}
	}
	affects(p, s.capacity(inv))
	p.Baseline = poolCreateFacts(s, inv, pools)
	p.Recheck = func(ctx context.Context) (mutate.Fingerprint, error) {
		inv, err := b.Devices(ctx)
		if err != nil {
			return nil, err
		}
		pools, err := b.R.PoolNames(ctx)
		if err != nil {
			return nil, err
		}
		return poolCreateFacts(s, inv, pools), nil
	}
	p.Irreversible = []string{"Whatever the devices held is gone; they belong to " + show(s.Name) + " until it is destroyed."}
	return p, nil
}

// options are zpool create's options for s, checked.
func (s PoolSpec) options() ([]string, error) {
	var o []string
	switch s.Ashift {
	case "":
	case "9", "12", "13", "14":
		o = append(o, "-o", "ashift="+s.Ashift)
	default:
		return nil, fmt.Errorf("ashift %s is not 9, 12, 13 or 14", show(s.Ashift))
	}
	if s.Autotrim {
		o = append(o, "-o", "autotrim=on")
	}
	if s.Compression != "" {
		v, err := checkProperty("compression", s.Compression)
		if err != nil {
			return nil, err
		}
		o = append(o, "-O", "compression="+v)
	}
	if s.Mountpoint != "" {
		v, err := checkProperty("mountpoint", s.Mountpoint)
		if err != nil {
			return nil, err
		}
		o = append(o, "-O", "mountpoint="+v)
	}
	return o, nil
}

// operands are the pool name and the vdev specification.
func (s PoolSpec) operands() []string {
	ops := []string{s.Name}
	if s.Layout != "" {
		ops = append(ops, s.Layout)
	}
	ops = append(ops, s.Data...)
	for _, role := range []struct {
		word string
		devs []string
	}{{"log", s.Log}, {"cache", s.Cache}, {"special", s.Special}, {"spare", s.Spare}} {
		if len(role.devs) == 0 {
			continue
		}
		ops = append(ops, role.word)
		if role.word == "special" && len(role.devs) > 1 {
			ops = append(ops, "mirror")
		}
		ops = append(ops, role.devs...)
	}
	return ops
}

func (s PoolSpec) all() []string {
	return slices.Concat(s.Data, s.Log, s.Cache, s.Special, s.Spare)
}

// judgeDevices blocks p for devices that are unknown, chosen twice,
// refused, or holding a signature without overwrite; with overwrite it
// lists what each one holds as destroyed. It reports whether any device
// holds a signature (and so needs zpool's -f).
func judgeDevices(p *mutate.Plan, paths []string, overwrite bool, inv []disks.Device) bool {
	seen, signatures := map[string]bool{}, false
	for _, path := range paths {
		blockIf(p, seen[path], show(path)+" is chosen twice")
		seen[path] = true
		d, ok := lookup(inv, path)
		if !ok {
			blockIf(p, true, show(path)+" is not a listed disk or partition")
			continue
		}
		v, why := disks.Judge(d, inv)
		switch {
		case v == disks.Refused:
			blockIf(p, true, show(path)+": "+why)
		case v == disks.Overwrite && !overwrite:
			blockIf(p, true, show(path)+" "+why+"; switch on overwrite to destroy it")
		case v == disks.Overwrite:
			// Writing over it is forced (-f): the name is typed.
			signatures, p.Flags.Force = true, true
			p.Preview.Destroys = append(p.Preview.Destroys, termtext.New(path+" ("+why+")"))
		}
	}
	return signatures
}

func lookup(inv []disks.Device, path string) (disks.Device, bool) {
	for _, d := range inv {
		if d.ByID == path || d.Path == path {
			return d, true
		}
	}
	return disks.Device{}, false
}

// capacity is the usable space the layout gives, roughly: raidz padding
// and the space ZFS keeps back are not counted.
func (s PoolSpec) capacity(inv []disks.Device) string {
	var sizes []uint64
	for _, path := range s.Data {
		if d, ok := lookup(inv, path); ok {
			sizes = append(sizes, d.Size)
		}
	}
	if len(sizes) == 0 {
		return "no data devices chosen"
	}
	smallest := slices.Min(sizes)
	var usable uint64
	switch s.Layout {
	case "":
		for _, n := range sizes {
			usable += n
		}
	case "mirror":
		usable = smallest
	default:
		if k := len(sizes) - parity[s.Layout]; k > 0 {
			usable = uint64(k) * smallest // k is positive
		}
	}
	line := "about " + iec(usable) + " usable, before ZFS's own reservations"
	if s.Layout != "" && slices.Max(sizes) != smallest {
		line += "; the smallest device (" + iec(smallest) + ") sets the size of each"
	}
	return line
}

// poolCreateFacts is what must not change between the preview and
// creating the pool: no pool of that name, and each device as listed.
func poolCreateFacts(s PoolSpec, inv []disks.Device, pools []string) mutate.Fingerprint {
	f := mutate.Fingerprint{"pool " + s.Name: mutate.Absent}
	if slices.Contains(pools, s.Name) {
		f["pool "+s.Name] = "imported"
	}
	maps.Copy(f, deviceFacts(s.all(), inv))
	return f
}

// deviceFacts are each chosen device as the inventory lists it.
func deviceFacts(paths []string, inv []disks.Device) mutate.Fingerprint {
	f := mutate.Fingerprint{}
	for _, path := range paths {
		d, ok := lookup(inv, path)
		if !ok {
			f["device "+path] = mutate.Absent
			continue
		}
		f["device "+path] = fmt.Sprintf("%s, %d bytes, serial %s, holds %q, mounted %v, held %t, pool %q",
			d.Name, d.Size, d.Serial, d.FSType, d.Mountpoints, d.Held, d.InPool)
	}
	return f
}

func layoutWord(l string) string {
	if l == "" {
		return "a stripe"
	}
	return "a " + l
}

func firstLine(err error) string {
	l, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	return l
}

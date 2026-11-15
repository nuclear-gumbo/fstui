// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package mutate

import "slices"

// Tier is how much a confirmation asks of the user: y (Low), y then
// enter (Medium), or typing the name (High).
type Tier int

// Tiers, in increasing order.
const (
	Low Tier = iota + 1
	Medium
	High
)

func (t Tier) String() string {
	switch t {
	case Low:
		return "low"
	case Medium:
		return "medium"
	case High:
		return "high"
	}
	return "unknown"
}

// Op is one operation in the tier table. The set is closed: a new
// operation needs a constant here and a row in table, and a test fails
// for a constant without one.
type Op int

// The operations fstui can run, plus enabling write mode in the app.
// Further operations are added with their plan builders.
const (
	EnableWrite Op = iota + 1
	SnapshotCreate
	SnapshotHold
	SnapshotRelease
	BookmarkCreate
	SnapshotClone
	SnapshotRename
	SnapshotDestroy
	Rollback
	Promote
	DatasetCreate
	DatasetRename
	DatasetDestroy
	PropertySet
	PropertyInherit
	Mount
	Unmount
	ScrubStart
	ScrubPause
	ScrubResume
	ScrubStop
	ErrorScrub
	TrimStart
	InitializeStart
	PoolClear
	DeviceOnline
	DeviceOffline
	DeviceClear
	VolumeCreate
	VolumeResize
	KeyLoad
	KeyUnload
	KeyChange
	ARCSet
	ARCPersist
	opEnd // not an operation: one past the last
)

// row is an operation's entry in the tier table.
type row struct {
	name   string // stable identifier for saved plans and the audit log
	base   Tier
	refine func(*Plan) Tier // optional: a higher tier from the plan's details
}

// table gives each operation above its tier.
var table = map[Op]row{
	EnableWrite:     {name: "enable-write", base: Medium},
	SnapshotCreate:  {name: "snapshot-create", base: Low},
	SnapshotHold:    {name: "snapshot-hold", base: Low},
	SnapshotRelease: {name: "snapshot-release", base: Low},
	BookmarkCreate:  {name: "bookmark-create", base: Low},
	SnapshotClone:   {name: "snapshot-clone", base: Low},
	SnapshotRename:  {name: "snapshot-rename", base: Medium},
	// One snapshot without clones or holds is Medium; several, a range or
	// recursive are High.
	SnapshotDestroy: {name: "snapshot-destroy", base: Medium, refine: func(p *Plan) Tier {
		if len(p.Targets) > 1 {
			return High
		}
		return Medium
	}},
	// Rollback to the latest snapshot is Medium; one that destroys
	// anything is raised by the escalation rules.
	Rollback:        {name: "rollback", base: Medium},
	Promote:         {name: "promote", base: Medium},
	DatasetCreate:   {name: "dataset-create", base: Low},
	DatasetRename:   {name: "dataset-rename", base: Medium},
	DatasetDestroy:  {name: "dataset-destroy", base: High},
	PropertySet:     {name: "property-set", base: Low, refine: propertyTier(setMedium)},
	PropertyInherit: {name: "property-inherit", base: Low, refine: propertyTier(inheritMedium)},
	Mount:           {name: "mount", base: Low},
	Unmount:         {name: "unmount", base: Medium},
	ScrubStart:      {name: "scrub-start", base: Low},
	ScrubPause:      {name: "scrub-pause", base: Low},
	ScrubResume:     {name: "scrub-resume", base: Low},
	// Stopping loses the progress so far.
	ScrubStop:       {name: "scrub-stop", base: Medium},
	ErrorScrub:      {name: "error-scrub", base: Low},
	TrimStart:       {name: "trim-start", base: Low},
	InitializeStart: {name: "initialize-start", base: Low},
	PoolClear:       {name: "pool-clear", base: Low},
	DeviceOnline:    {name: "device-online", base: Low},
	// High when the vdev is left without redundancy (escalation rules).
	DeviceOffline: {name: "device-offline", base: Medium},
	DeviceClear:   {name: "device-clear", base: Low},
	VolumeCreate:  {name: "volume-create", base: Low},
	// Growing is Medium; shrinking loses the data past the new size.
	VolumeResize: {name: "volume-resize", base: Medium, refine: func(p *Plan) Tier {
		if p.Params["shrink"] == "true" {
			return High
		}
		return Medium
	}},
	KeyLoad: {name: "key-load", base: Low},
	// Unloading unmounts everything under the encryption root.
	KeyUnload: {name: "key-unload", base: Medium},
	KeyChange: {name: "key-change", base: Medium},
	// Cannot go back to automatic without a reload; the ARC shrinks
	// only under pressure.
	ARCSet:     {name: "arc-set", base: Medium},
	ARCPersist: {name: "arc-persist", base: Medium},
}

// Properties whose change is Medium: layout properties, and those that affect
// running workloads or free space.
var (
	setMedium = []string{
		"recordsize", "dedup", "special_small_blocks",
		"mountpoint", "canmount", "readonly", "quota", "refquota", "reservation", "refreservation",
	}
	inheritMedium = []string{"mountpoint", "canmount", "readonly", "sharenfs", "sharesmb"}
)

func propertyTier(medium []string) func(*Plan) Tier {
	return func(p *Plan) Tier {
		if slices.Contains(medium, p.Property) {
			return Medium
		}
		return Low
	}
}

func (o Op) String() string {
	if r, ok := table[o]; ok {
		return r.name
	}
	return "unknown"
}

// OpNamed returns the operation with the given name, for saved plans.
func OpNamed(name string) (Op, bool) {
	for o, r := range table {
		if r.name == name {
			return o, true
		}
	}
	return 0, false
}

// destroys reports whether the operation is a destroy in the sense of
// the escalation rules.
func (o Op) destroys() bool { return o == SnapshotDestroy || o == DatasetDestroy }

// TierOf is the one place a plan's tier is decided: the table entry,
// refined by the plan's details, raised by the escalation rules.
func TierOf(p *Plan) Tier {
	r, ok := table[p.Op]
	if !ok {
		return High // unreachable while the table test passes; fail safe
	}
	t := r.base
	if r.refine != nil {
		t = max(t, r.refine(p))
	}
	return max(t, escalation(p))
}

// escalation applies the rules after the table lookup.
func escalation(p *Plan) Tier {
	switch {
	case p.Flags.Force,
		p.Op.destroys() && (p.Flags.Recursive || p.Flags.RecursiveDependents),
		p.Op.destroys() && p.Preview.Outside,
		p.Op == Rollback && len(p.Preview.Destroys) > 0,
		p.Flags.RedundancyLoss:
		return High
	case len(p.Targets) > 1:
		return Medium
	}
	return Low
}

// Range is the least and most an operation can ask before it is known
// what it applies to: one snapshot destroy is Medium, several High.
func Range(o Op) (least, most Tier) {
	r, ok := table[o]
	if !ok {
		return High, High
	}
	least, most = r.base, r.base
	switch o { //nolint:exhaustive // the ones whose tier depends on the details
	case SnapshotDestroy, Rollback, DeviceOffline, VolumeResize, Unmount:
		most = High
	case PropertySet, PropertyInherit, SnapshotCreate, SnapshotHold, SnapshotRelease:
		most = max(most, Medium) // layout properties; several targets
	}
	return least, most
}

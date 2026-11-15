// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

// Package mutate is the mutation pipeline: plans, their tiers, the re-check
// before running, and saved plans. It starts no processes itself;
// execute.Mutator runs a plan's steps.
//
// The package is left out of builds with the fstui_nowrite tag.
package mutate

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"maps"
	"slices"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Plan is one change, built before anything runs: what it does, the
// exact commands, what it would destroy or affect, and how to tell
// whether the system still looks as it did when the plan was made.
type Plan struct {
	ID      uint64
	Op      Op
	Summary string   // "Create snapshot tank/home@before-upgrade"
	Targets []string // the objects the user selected; the high tier asks for Targets[0] to be typed
	// TypeToConfirm, if set, is what the high tier asks for instead of
	// Targets[0], such as "destroy 3 snapshots" for several.
	TypeToConfirm string
	// Property is the property a PropertySet or PropertyInherit plan
	// changes; it refines the tier.
	Property string
	Steps    []execute.Argv
	// Stdin is given to every step: a passphrase, never logged or
	// shown. Run zeroes it after the steps return.
	Stdin   []byte
	Flags   Flags
	Preview Preview
	// Blocked lists reasons the plan cannot run, such as a hold on a
	// snapshot to destroy; a blocked plan is shown but not confirmable.
	Blocked      []string
	Irreversible []string // plain-language consequences, shown verbatim
	Recheck      func(context.Context) (Fingerprint, error)
	Baseline     Fingerprint
	// Params are the operation's parameters by name, from which the
	// plan can be built again: what a saved plan stores.
	Params map[string]string
	// SavedSHA256 identifies the saved plan this one was rebuilt from,
	// for the audit log; empty for a plan made now.
	SavedSHA256 string
	// Secret is set when Stdin carries a passphrase: such a plan is not
	// saved, since a saved plan is a file and cannot be applied without
	// its secret.
	Secret bool
}

// Flags are the options that change a plan's tier.
type Flags struct {
	Force               bool
	Recursive           bool // -r
	RecursiveDependents bool // -R
	RedundancyLoss      bool // leaves a vdev without redundancy
}

// Preview is the blast radius, from the native dry run where one exists.
type Preview struct {
	Destroys []termtext.Text // datasets, snapshots, bookmarks, clones
	Affects  []termtext.Text // children unmounted, dependents, names created
	Frees    int64           // bytes, from zfs destroy -nvp; -1 unknown
	// Outside is set when a destroy reaches objects outside the selected
	// hierarchy (escalates to High).
	Outside bool
	// HiddenBySelection counts selected items the active filter hides.
	HiddenBySelection int
	// Output is the native dry run's output, shown as it came.
	Output termtext.Text
}

// NewID returns a random plan ID for the audit log.
func NewID() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	return binary.BigEndian.Uint64(b[:])
}

// Fingerprint is the state a plan depends on: per object, the facts
// that must not change between preview and running, such as its GUID
// and createtxg, or Absent for a name that must not exist.
type Fingerprint map[string]string

// Absent is the fingerprint of an object that does not exist.
const Absent = "absent"

// Diff describes how now differs from f, one line per object, sorted;
// empty when they are the same.
func (f Fingerprint) Diff(now Fingerprint) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(f)) {
		v, ok := now[k]
		switch {
		case !ok || v == Absent && f[k] != Absent:
			out = append(out, fmt.Sprintf("%s no longer exists", termtext.New(k).Display()))
		case f[k] == Absent && v != Absent:
			out = append(out, fmt.Sprintf("%s now exists (%s)", termtext.New(k).Display(), termtext.New(v).Display()))
		case v != f[k]:
			out = append(out, fmt.Sprintf("%s changed (%s, was %s)",
				termtext.New(k).Display(), termtext.New(v).Display(), termtext.New(f[k]).Display()))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(now)) {
		if _, ok := f[k]; !ok {
			out = append(out, fmt.Sprintf("%s now exists", termtext.New(k).Display()))
		}
	}
	return out
}

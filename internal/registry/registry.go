// SPDX-License-Identifier: GPL-3.0-or-later

// Package registry records which optional modules a build of fstui includes.
//
// Modules are left out at build time with opt-out tags. cmd/fstui
// registers each compiled-in module explicitly from main, so the set of
// modules is visible in one place and does not depend on init order.
package registry

import (
	"context"
	"slices"
	"strings"
)

// Kind separates storage backends from cross-cutting modules such as write
// support, because a build needs at least one backend to be useful.
type Kind int

// Module kinds.
const (
	Backend Kind = iota + 1
	Feature
)

// Module describes one optional part of fstui.
type Module struct {
	Name    string // short name shown to users and used as the Gentoo USE flag, e.g. "zfs"
	OmitTag string // build tag that leaves the module out, e.g. "fstui_nozfs"
	Kind    Kind
	// Describe, if set, reports the module's state on this host in one
	// line for --version, e.g. the OpenZFS release found.
	Describe func(context.Context) string
}

// Registry is the set of modules compiled into this binary.
type Registry struct {
	modules []Module
}

// Add records a compiled-in module. Adding the same name twice is a
// programming error in cmd/fstui and panics, so it fails every test run
// rather than producing a confusing module list.
func (r *Registry) Add(m Module) {
	if slices.ContainsFunc(r.modules, func(x Module) bool { return x.Name == m.Name }) {
		panic("registry: module registered twice: " + m.Name)
	}
	r.modules = append(r.modules, m)
}

// Modules returns the registered modules in registration order.
func (r *Registry) Modules() []Module {
	return slices.Clone(r.modules)
}

// HasBackend reports whether at least one storage backend is compiled in.
func (r *Registry) HasBackend() bool {
	return slices.ContainsFunc(r.modules, func(m Module) bool { return m.Kind == Backend })
}

// Summary lists module names in registration order, e.g. "zfs, write",
// or "none".
func (r *Registry) Summary() string {
	if len(r.modules) == 0 {
		return "none"
	}
	names := make([]string, len(r.modules))
	for i, m := range r.modules {
		names[i] = m.Name
	}
	return strings.Join(names, ", ")
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_nowrite

package main

import (
	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// Builds without the write module (fstui_nowrite) contain no code that
// changes storage: these stand in for module_write.go.

func registerWrite(*registry.Registry) {}

const writeUsage = "\nThis build only reads; it cannot change anything.\n"

type writeFlags struct{}

func addWriteFlags(*pflag.FlagSet) *writeFlags { return &writeFlags{} }

func checkWriteFlags(*writeFlags) error { return nil }

type writeHooks struct{}

func startWrite(*writeFlags, writeHooks) (ui.Actions, []ui.Screen, string, func(), []string) {
	return nil, nil, "", func() {}, nil
}

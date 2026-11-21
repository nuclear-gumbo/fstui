// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && fstui_nowrite

package main

import (
	"github.com/nuclear-gumbo/fstui/internal/demo"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// demoWrite has nothing to give in a demo built without the write module.
func demoWrite(*demo.Commands, *zfs.Reader, *demo.Schedules) writeHooks { return writeHooks{} }

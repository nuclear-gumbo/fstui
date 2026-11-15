// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nozfs && fstui_nowrite && !fstui_demo

package main

import (
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// zfsWrite has nothing to give in builds without the write module.
func zfsWrite(*zfs.Reader, *execute.Tools) writeHooks { return writeHooks{} }

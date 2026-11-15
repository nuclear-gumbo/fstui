// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_nozfs && !fstui_demo

package main

import (
	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/registry"
)

// registerZFS is empty in builds that leave out the ZFS backend.
func registerZFS(*registry.Registry) {}

// zfsScreens has nothing to open in builds without the ZFS backend.
func zfsScreens(config.Config) (opened, error) { return opened{}, nil }

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_nozfs && !fstui_demo

package main

import (
	"context"
	"errors"

	"github.com/nuclear-gumbo/fstui/internal/notify"

	"github.com/nuclear-gumbo/fstui/internal/check"
	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/registry"
)

// registerZFS is empty in builds that leave out the ZFS backend.
func registerZFS(*registry.Registry) {}

// zfsScreens has nothing to open in builds without the ZFS backend.
func zfsScreens(config.Config) (opened, error) { return opened{}, nil }

// checkSource has no pools to look at in builds without the ZFS backend.
func checkSource(config.Config) (check.Source, error) {
	return nil, errors.New("this build of fstui has no ZFS backend")
}

// checkSending is how fstui check and watch send, and where they keep
// what they sent: for real, in dir.
func checkSending(dir string) (func(context.Context, notify.Channel, notify.Message) error, string) {
	return notify.Send, dir
}

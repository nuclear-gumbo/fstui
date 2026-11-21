// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nozfs || fstui_demo

package main

import (
	"context"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// poolNames lists the pools with zpool list; nothing when it fails, as
// the list only completes a hint.
func poolNames(r *zfs.Reader, timeout time.Duration) func() []string {
	return func() []string {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		names, _ := r.PoolNames(ctx) // a hint: a failure leaves it out
		return names
	}
}

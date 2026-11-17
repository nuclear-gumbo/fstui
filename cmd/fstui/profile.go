// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_profile

package main

import (
	"os"
	"runtime"
	"runtime/pprof"
	"time"
)

// A build for measuring memory (tag fstui_profile, never shipped) writes a
// heap profile to $FSTUI_HEAPPROFILE every 5 seconds, after a collection,
// for go tool pprof. CONTRIBUTING.md says how it is used.
func init() {
	path := os.Getenv("FSTUI_HEAPPROFILE")
	if path == "" {
		return
	}
	go func() {
		for range time.Tick(5 * time.Second) {
			runtime.GC()
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // a path the developer chose
			if err != nil {
				return
			}
			_ = pprof.WriteHeapProfile(f)
			_ = f.Close()
		}
	}()
}

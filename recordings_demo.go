// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

// Package fstui holds the OpenZFS output recorded for the tests. Only the
// demo build (tag fstui_demo) compiles it: it replays these recordings
// instead of running zfs and zpool. Other builds contain none of it.
package fstui

import "embed"

// Recorded is testdata/zfs/2.3.9: output of OpenZFS 2.3.9 on throwaway
// pools, captured by scripts/capture-golden.sh.
//
//go:embed testdata/zfs/2.3.9 testdata/systemd/257
var Recorded embed.FS

// RecordedRoot is the directory inside Recorded.
const RecordedRoot = "testdata/zfs/2.3.9"

// RecordedTimers is systemctl list-timers --output=json from Debian 13
// with two OpenZFS scrub timers enabled.
const RecordedTimers = "testdata/systemd/257/list-timers.json"

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

// A scrub of a mirror with one side overwritten reports checksum errors
// on that side; the reader finds them as errors, with the device, and the
// scrub's start and finish.
func TestIntegrationEvents(t *testing.T) {
	tp := testpool.New(t, "mirror", 2)
	// Where ZFS puts the data on the device is its choice. The test
	// writes 128 MiB, makes sure it is on disk, and overwrites 192 MiB of
	// one side after its front labels, so the scrub must meet bad blocks
	// whatever the layout (a fixed 32 MiB range passed here and missed
	// the data on one test machine).
	file := filepath.Join(testpool.Dir, "mnt", tp.Name, "data")
	if err := os.WriteFile(file, bytes.Repeat([]byte("fstui events "), 10<<20), 0o600); err != nil { // 130 MiB
		t.Fatal(err)
	}
	tp.Sync()
	tp.Corrupt(1, 4<<20, 192<<20)
	tp.Scrub()

	r := NewReader(runner(t))
	evs, _, err := r.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var checksums, scrubs int
	for _, e := range evs {
		if e.Source.Raw() != tp.Name {
			continue
		}
		switch e.Class.Raw() {
		case "ereport.fs.zfs.checksum":
			checksums++
			if e.Severity != events.Error || e.Device.Raw() != tp.Images[1] {
				t.Errorf("checksum event: %s on %q", e.Severity, e.Device.Raw())
			}
		case "sysevent.fs.zfs.scrub_start", "sysevent.fs.zfs.scrub_finish":
			scrubs++
		}
	}
	if checksums == 0 || scrubs != 2 {
		t.Errorf("%d checksum events and %d scrub events for %s", checksums, scrubs, tp.Name)
	}
}

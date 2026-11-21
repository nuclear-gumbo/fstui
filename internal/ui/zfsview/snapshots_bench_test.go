// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"fmt"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// benchSnapshots draws Snapshots with 100,000 snapshots; cold drops the
// kept sort first, as every draw did before 0.6.0.
func benchSnapshots(b *testing.B, cold bool) {
	data := make([]zfs.SnapshotRow, 100000)
	for i := range data {
		data[i] = zfs.SnapshotRow{Name: fmt.Sprintf("tank/home/u%d@auto-%06d", i%50, i), CreateTXG: uint64(100000 - i), Used: uint64(i), Referenced: 5, Creation: 1794800000}
	}
	v := NewSnapshots(nil, nil, time.Now)
	v.poll.data, v.poll.loaded = data, time.Now()
	d := ui.Display{}
	b.ReportAllocs()
	for b.Loop() {
		if cold {
			v.order.ok = false
		}
		_ = v.Render(d, 160, 50)
	}
}

func BenchmarkSnapshotsRenderCached(b *testing.B)   { benchSnapshots(b, false) }
func BenchmarkSnapshotsRenderUncached(b *testing.B) { benchSnapshots(b, true) }

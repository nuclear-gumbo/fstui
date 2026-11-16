// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"fmt"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// benchSnapshots draws Snapshots with 100,000 snapshots; cold drops the
// kept sort first, as every draw did before 0.6.0.
func benchSnapshots(b *testing.B, cold bool) {
	data := make([]zfs.Dataset, 100000)
	for i := range data {
		data[i] = zfs.Dataset{Name: fmt.Sprintf("tank/home/u%d@auto-%06d", i%50, i), Type: zfs.Snapshot, CreateTXG: uint64(100000 - i), Properties: zfs.Properties{
			"used": {Number: uint64(i), IsNumber: true, Text: termtext.New("1")}, "referenced": {Number: 5, IsNumber: true}, "creation": {Number: 1794800000, IsNumber: true},
		}}
	}
	v := NewSnapshots(nil, time.Now)
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

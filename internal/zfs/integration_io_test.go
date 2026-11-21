// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

// zpool iostat streamed from a real pool under writes: the sample shows
// the pool's writes, its mirror and both disks, with write latency.
func TestIntegrationIOStats(t *testing.T) {
	tp := testpool.New(t, "mirror", 2)
	r := NewReader(runner(t))
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	// The writer must have stopped before the test ends: the pool's
	// cleanup cannot unmount a dataset with an open file ("pool or
	// dataset is busy").
	writerDone := make(chan struct{})
	defer func() {
		cancel()
		<-writerDone
	}()
	go func() {
		defer close(writerDone)
		data := bytes.Repeat([]byte("fstui"), 1<<20) // 5 MiB
		file := filepath.Join(testpool.Dir, "mnt", tp.Name, "data")
		for ctx.Err() == nil {
			f, err := os.Create(file) // inside the test pool's mountpoint
			if err != nil {
				return
			}
			_, _ = f.Write(data)
			_ = f.Sync()
			_ = f.Close()
		}
	}()

	var found *IOPool
	err := r.IOStats(ctx, func(s IOSample) {
		for i := range s {
			p := s[i]
			if p.Name.Raw() == tp.Name && p.WriteBytes > 0 && p.HasWriteWait && len(p.Devices) == 3 {
				found = &p
				cancel()
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("no interval showed writes to the test pool within a minute")
	}
	mirror, disk := found.Devices[0], found.Devices[1]
	if mirror.Name.Raw() != "mirror-0" || mirror.Depth != 1 || disk.Depth != 2 || disk.Name.Raw() != tp.Images[0] {
		t.Errorf("devices: %+v", found.Devices)
	}
}

// The ARC statistics of the running kernel parse, and their counters
// only grow.
func TestIntegrationARCStats(t *testing.T) {
	a, err := ReadARCStats()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	b, err := ReadARCStats()
	if err != nil {
		t.Fatal(err)
	}
	if a.Max == 0 || a.Min > a.Max || a.Memory == 0 || b.Hits < a.Hits || b.Misses < a.Misses {
		t.Errorf("ARC readings: %+v then %+v", a, b)
	}
}

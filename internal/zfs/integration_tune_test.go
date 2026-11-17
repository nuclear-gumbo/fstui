// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/testpool"
	"github.com/nuclear-gumbo/fstui/internal/tune"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Tune mode's reads on a real pool: kstat samples before and after a
// write, and zpool iostat streamed while it runs, summed in a window.
func TestIntegrationTune(t *testing.T) {
	p := testpool.New(t, "mirror", 2)
	b, _, _, _ := writeSetup(t)
	r, ok := b.R.(*zfs.Reader)
	if !ok {
		t.Fatalf("reader %T", b.R)
	}
	pool, err := name.ParsePool(p.Name)
	if err != nil {
		t.Fatal(err)
	}
	var w tune.Window
	var mu sync.Mutex
	first, err := r.ReadTuneSample(pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w.Sample(first)

	ctx, cancel := context.WithTimeout(t.Context(), 4500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- r.TuneIO(ctx, pool, time.Second, func(rep zfs.IOPool) { mu.Lock(); w.AddIO(rep, 1); mu.Unlock() })
	}()
	fill(t, p, 64)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	last, err := r.ReadTuneSample(pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w.Sample(last)
	if n, ok := w.Objset(p.Name, "nwritten"); !ok || n < 64<<20 {
		t.Errorf("the pool's root dataset wrote %d, %v", n, ok)
	}
	if w.Seconds < 2 || w.IO[p.Name] == nil || w.IO[p.Name].WriteBytes == 0 || w.IO[p.Name].TotalWait[1] == 0 {
		t.Errorf("streamed %v s: %+v", w.Seconds, w.IO[p.Name])
	}
}

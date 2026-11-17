// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// phase reads a recorded phase of testdata/zfs/2.3.9/tune as a sample.
func phase(t *testing.T, dir string, at time.Time) zfs.TuneSample {
	t.Helper()
	root := filepath.Join("../../testdata/zfs/2.3.9/tune", dir)
	r := zfs.NewReader(nil)
	r.ReadDir = func(string) ([]string, error) {
		entries, err := os.ReadDir(root)
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out, err
	}
	r.ReadFile = func(p string) ([]byte, error) { return os.ReadFile(filepath.Join(root, filepath.Base(p))) }
	pool, _ := name.ParsePool("fstuitest_tune")
	s, err := r.ReadTuneSample(pool, at)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWindowObjsetDeltas(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	var w Window
	w.Sample(phase(t, "1-seqwrite", t0))
	w.Sample(phase(t, "2-smallwrite", t0.Add(time.Minute)))
	if n, ok := w.Objset("fstuitest_tune/small", "writes"); !ok || n != 2000 {
		t.Errorf("small writes %d, %v", n, ok)
	}
	if n, ok := w.Objset("fstuitest_tune/seq", "nwritten"); !ok || n != 0 {
		t.Errorf("seq wrote %d during the small writes, %v", n, ok)
	}
	// The re-import resets every counter: the window starts again.
	w.Sample(phase(t, "5-cold", t0.Add(2*time.Minute)))
	if !w.Restarted || !w.Start.At.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("restarted %v at %v", w.Restarted, w.Start.At)
	}
	w.Sample(phase(t, "6-reads", t0.Add(3*time.Minute)))
	if n, ok := w.Objset("fstuitest_tune/seq", "nread"); !ok || n != 200<<20 {
		t.Errorf("seq read %d, %v", n, ok)
	}
	if w.Global(func(s zfs.TuneSample) zfs.Kstat { return s.ARC }, "misses") == 0 {
		t.Error("no ARC misses reading from a cold cache")
	}
}

func TestWindowSumsIO(t *testing.T) {
	var w Window
	rep := ioPool(zfs.IOStat{Name: termtext.New("sda"), WriteOps: 100, WriteBytes: 1 << 20, WriteWait: 2000, WriteDiskWait: 500})
	w.AddIO(rep, 5)
	w.AddIO(rep, 5)
	v := w.IO["sda"]
	if w.Seconds != 10 || v.WriteBytes != 10<<20 || v.WriteOps != 1000 || v.TotalWait[1]/v.WriteOps != 2000 || v.DiskWait[1]/v.WriteOps != 500 || w.IO["tank"] == nil {
		t.Errorf("seconds %v, %+v", w.Seconds, v)
	}
}

// ioPool is an interval of pool tank with the given device rows.
func ioPool(devices ...zfs.IOStat) zfs.IOPool {
	return zfs.IOPool{IOStat: zfs.IOStat{Name: termtext.New("tank")}, Devices: devices}
}

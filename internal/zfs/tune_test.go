// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// streamCommands streams the recorded zpool iostat -l interval output,
// line by line.
type streamCommands struct {
	t    *testing.T
	seen []string
}

func (*streamCommands) Read(context.Context, execute.Argv) (execute.Result, error) {
	return execute.Result{}, errors.New("not recorded")
}

func (s *streamCommands) Stream(_ context.Context, a execute.Argv, line func([]byte)) error {
	s.seen = append(s.seen, a.String())
	for _, l := range bytes.Split(golden(s.t, "tune", "interval", "zpool-iostat-l.txt"), []byte("\n")) {
		line(l)
	}
	return nil
}

// Both recorded intervals reach the caller, with the pool's devices and
// their disk waits.
func TestTuneIOReports(t *testing.T) {
	cmds := &streamCommands{t: t}
	r := NewReader(cmds)
	pool, _ := name.ParsePool("fstuitest_tune")
	var n int
	err := r.TuneIO(t.Context(), pool, 5*time.Second, func(p IOPool) {
		n++
		if len(p.Devices) != 4 || p.WriteBytes == 0 || p.Devices[1].WriteDiskWait == 0 || p.Devices[1].WriteWait < p.Devices[1].WriteDiskWait {
			t.Errorf("interval %+v", p)
		}
	})
	if err != nil || n != 2 {
		t.Fatalf("%d intervals, %v", n, err)
	}
	if len(cmds.seen) != 1 || cmds.seen[0] != "zpool iostat -p -l -v -y -- fstuitest_tune 5" {
		t.Errorf("commands %q", cmds.seen)
	}
}

// A sample read through the file hooks from the recording of a phase.
func TestReadTuneSample(t *testing.T) {
	dir := "../../testdata/zfs/2.3.9/tune/6-reads"
	r := NewReader(&streamCommands{t: t})
	r.ReadDir = func(string) ([]string, error) {
		entries, err := os.ReadDir(dir)
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out, err
	}
	r.ReadFile = func(p string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, path.Base(p))) }
	pool, _ := name.ParsePool("fstuitest_tune")
	s, err := r.ReadTuneSample(pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Objsets) != 5 || len(s.Txgs) == 0 {
		t.Errorf("%d objsets, %d txgs", len(s.Objsets), len(s.Txgs))
	}
	if n, ok := s.Objsets["fstuitest_tune/small"].Uint("reads"); !ok || n != 1000 {
		t.Errorf("small reads %d, %v", n, ok)
	}
	if _, ok := s.ARC.Uint("mru_ghost_hits"); !ok {
		t.Error("arcstats lacks mru_ghost_hits")
	}
	if _, ok := s.DMUTx.Uint("dmu_tx_dirty_throttle"); !ok {
		t.Error("dmu_tx lacks dmu_tx_dirty_throttle")
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// Tune mode's kernel statistics in the demo are replayed from a real
// pool's, recorded on OpenZFS 2.3.9 through seven phases of known
// workloads (testdata/zfs/2.3.9/tune). Each sample a pool's Tune tab
// takes moves it one phase on, so the counters grow as a working pool's
// do, then stay, as on a pool gone quiet; the tab measures, judges and
// points to changes as on a real system. The recorded datasets become
// the pool's own.

// tunePhases are the recorded phases the demo replays, in the order they
// were taken. The recording's later phases (5-cold, 6-reads) followed an
// export and import that reset the counters, so they would restart the
// window; the replay stops at the last phase before them.
var tunePhases = []string{"0-base", "1-seqwrite", "2-smallwrite", "3-syncwrite", "4-text"}

// tuneRecorded is the recorded pool, whose datasets map onto the demo's.
const tuneRecorded = "fstuitest_tune"

// tuneReplay is where one pool's replay stands.
type tuneReplay struct {
	phase    int
	datasets map[string]string // recorded dataset -> the pool's
}

// kstatPool returns the pool a path under the kstat directory names, and
// the file below it, if it is one of the demo's pools.
func (c *Commands) kstatPool(p string) (pool, file string, ok bool) {
	rest, found := strings.CutPrefix(p, zfs.KstatDir+"/")
	if !found {
		return "", "", false
	}
	pool, file, _ = strings.Cut(rest, "/")
	return pool, file, slices.Contains(c.poolNames(), pool)
}

// tuneDir lists a pool's kstat directory, starting the pool's next
// sample: the objset files of the phase and its txgs.
func (c *Commands) tuneDir(pool string) ([]string, error) {
	st, err := c.load()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tune == nil {
		c.tune = map[string]*tuneReplay{}
	}
	r := c.tune[pool]
	if r == nil {
		r = &tuneReplay{phase: -1, datasets: tuneDatasets(st, pool)}
		c.tune[pool] = r
	}
	r.phase = min(r.phase+1, len(tunePhases)-1)
	c.tuneLast = pool
	entries, err := fs.ReadDir(fstui.Recorded, path.Join(fstui.RecordedRoot, "tune", tunePhases[r.phase]))
	if err != nil {
		return nil, fmt.Errorf("demo recording: %w", err)
	}
	var out []string
	for _, e := range entries {
		switch {
		case e.Name() == "txgs":
			out = append(out, e.Name())
		case strings.HasPrefix(e.Name(), "objset-0x"):
			b, err := fs.ReadFile(fstui.Recorded, path.Join(fstui.RecordedRoot, "tune", tunePhases[r.phase], e.Name()))
			if err != nil {
				return nil, fmt.Errorf("demo recording: %w", err)
			}
			if _, mapped := r.datasets[recordedDataset(b)]; mapped {
				out = append(out, e.Name())
			}
		}
	}
	return out, nil
}

// tuneDatasets maps the recorded datasets onto pool's: its root, then
// its filesystems in order.
func tuneDatasets(st *state, pool string) map[string]string {
	var own []string
	for _, n := range st.sorted() {
		if e := st.entries[n]; e.Pool == pool && e.Type == "FILESYSTEM" && n != pool {
			own = append(own, n)
		}
	}
	// Each recorded dataset becomes a different one of the pool's: two
	// recordings under one name would make its counters jump about. A
	// pool with fewer filesystems replays fewer datasets.
	m := map[string]string{tuneRecorded: pool}
	for i, child := range []string{"seq", "small", "sync", "text"} {
		if i < len(own) {
			m[tuneRecorded+"/"+child] = own[i]
		}
	}
	return m
}

// recordedDataset is the dataset_name of a recorded objset kstat.
func recordedDataset(b []byte) string {
	for line := range bytes.Lines(b) {
		if f := strings.Fields(string(line)); len(f) == 3 && f[0] == "dataset_name" {
			return f[2]
		}
	}
	return ""
}

// tuneFile reads a kstat file of the phase a pool's replay is at: one of
// the pool's, or a global one (dmu_tx, zil, zfetchstats) for the pool
// last sampled.
func (c *Commands) tuneFile(pool, file string) ([]byte, bool, error) {
	c.mu.Lock()
	if pool == "" {
		pool = c.tuneLast
	}
	r := c.tune[pool]
	c.mu.Unlock()
	if r == nil {
		return nil, false, nil
	}
	b, err := fs.ReadFile(fstui.Recorded, path.Join(fstui.RecordedRoot, "tune", tunePhases[r.phase], file))
	if err != nil {
		return nil, true, fmt.Errorf("demo recording: %w", err)
	}
	if strings.HasPrefix(file, "objset-0x") {
		for rec, own := range r.datasets {
			b = bytes.Replace(b, []byte(" "+rec+"\n"), []byte(" "+own+"\n"), 1)
		}
	}
	return b, true, nil
}

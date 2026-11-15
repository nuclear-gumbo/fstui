// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/nuclear-gumbo/fstui"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

const mib = 1 << 20

// load is what each demo pool is doing, in bytes per second on average,
// to match its recorded state: media is being scrubbed, archive is
// resilvering onto a new disk, tank is busy.
var load = map[string]struct{ read, write float64 }{
	"tank":    {40 * mib, 15 * mib},
	"media":   {120 * mib, 0.2 * mib},
	"archive": {60 * mib, 60 * mib},
	"fast":    {8 * mib, 25 * mib},
	"backup":  {0.5 * mib, 5 * mib},
	"vault":   {0.3 * mib, 0.1 * mib},
	"scratch": {2 * mib, 1 * mib},
}

// slowDevice is a disk that answers more slowly than its siblings, which
// the I/O view exists to spot.
const slowDevice = "/dev/disk/by-id/ata-DEMO_TANK_B"

// Stream implements zfs.Commands: zpool iostat prints made-up activity
// for the recorded pools once a second until ctx is cancelled.
func (c *Commands) Stream(ctx context.Context, a execute.Argv, line func([]byte)) error {
	if a.Tool != execute.Zpool || len(a.Options) == 0 || a.Options[0] != "iostat" {
		return fmt.Errorf("%w: %s", errNoRecording, a)
	}
	m, err := c.ioModel()
	if err != nil {
		return err
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			for _, l := range m.interval() {
				line([]byte(l))
			}
		}
	}
}

type ioModel struct {
	pools []zfs.PoolStatus
	rng   *rand.Rand
	level map[string]float64 // each pool's current share of its load, a random walk
}

func (c *Commands) ioModel() (*ioModel, error) {
	c.mu.Lock()
	m := c.io
	c.mu.Unlock()
	if m != nil {
		return m, nil
	}
	b, err := c.pools("zpool-status.json", nil)
	if err != nil {
		return nil, err
	}
	st, err := zfs.ParsePoolStatus(b)
	if err != nil {
		return nil, err
	}
	m = &ioModel{pools: st, rng: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1)), level: map[string]float64{}} //nolint:gosec // made-up activity
	c.mu.Lock()
	c.io = m
	c.mu.Unlock()
	return m, nil
}

// row is one line of zpool iostat -p -l -v output; parsing keeps only the
// last 17 fields, so alignment is for people reading the recording.
type row struct {
	name                 string
	rOps, wOps, rBW, wBW uint64
	rWait, wWait         time.Duration
}

func (r row) String() string {
	lat := func(d time.Duration, ops uint64) string {
		if ops == 0 {
			return "-"
		}
		return fmt.Sprint(d.Nanoseconds())
	}
	vals := []string{
		"0", "0", fmt.Sprint(r.rOps), fmt.Sprint(r.wOps), fmt.Sprint(r.rBW), fmt.Sprint(r.wBW),
		lat(r.rWait, r.rOps), lat(r.wWait, r.wOps),
	}
	for range 9 { // disk, syncq and asyncq wait, scrub, trim, rebuild
		vals = append(vals, "-")
	}
	return fmt.Sprintf("%-48s %s", r.name, strings.Join(vals, "  "))
}

func (r *row) add(o row) {
	r.rOps, r.wOps, r.rBW, r.wBW = r.rOps+o.rOps, r.wOps+o.wOps, r.rBW+o.rBW, r.wBW+o.wBW
	r.rWait, r.wWait = max(r.rWait, o.rWait), max(r.wWait, o.wWait)
}

const dashes = "------------------------------------------------  ---  ---  ---  ---"

// interval prints one second of activity for every pool.
func (m *ioModel) interval() []string {
	out := []string{
		strings.Repeat(" ", 40) + "capacity     operations     bandwidth     total_wait",
		"pool                                             alloc  free  read  write  read  write  read  write",
		dashes,
	}
	for _, p := range m.pools {
		name := p.Name.String()
		lv := m.level[name]
		if lv == 0 {
			lv = 1
		}
		lv = min(max(lv+m.rng.NormFloat64()*0.15, 0.2), 1.8) //nolint:mnd // a gentle random walk
		m.level[name] = lv
		l := load[name]
		leaves := countLeaves(p.Data.Children)
		var lines []string
		total := row{name: name}
		for _, v := range p.Data.Children {
			r, ls := m.device(v, 1, "", l.read*lv/float64(max(leaves, 1)), l.write*lv/float64(max(leaves, 1)))
			total.add(r)
			lines = append(lines, ls...)
		}
		for _, sec := range []struct {
			label string
			vdevs []zfs.Vdev
		}{{"special", p.Special}, {"dedup", p.Dedup}, {"logs", p.Logs}, {"cache", p.Cache}} {
			if len(sec.vdevs) == 0 {
				continue
			}
			lines = append(lines, row{name: sec.label}.label())
			for _, v := range sec.vdevs {
				r, ls := m.device(v, 1, sec.label, l.read*lv*0.1, l.write*lv*0.2) //nolint:mnd // support devices see less
				total.add(r)
				lines = append(lines, ls...)
			}
		}
		out = append(out, total.String())
		out = append(out, lines...)
		out = append(out, dashes)
	}
	return out
}

func (r row) label() string {
	return fmt.Sprintf("%-48s %s", r.name, strings.TrimSpace(strings.Repeat("-  ", 17)))
}

func countLeaves(vs []zfs.Vdev) int {
	n := 0
	for _, v := range vs {
		if len(v.Children) == 0 {
			n++
		} else {
			n += countLeaves(v.Children)
		}
	}
	return n
}

// device returns a vdev's row, summed from its children, and the lines
// for it and everything below it.
func (m *ioModel) device(v zfs.Vdev, depth int, section string, read, write float64) (row, []string) {
	indent := strings.Repeat("  ", depth)
	if len(v.Children) == 0 {
		r := row{name: indent + v.Name.Display()}
		if v.State == "ONLINE" || v.State == "" {
			r.rBW = uint64(read * (0.8 + 0.4*m.rng.Float64()))  //nolint:mnd // jitter
			r.wBW = uint64(write * (0.8 + 0.4*m.rng.Float64())) //nolint:mnd // jitter
			r.rOps, r.wOps = r.rBW/(64<<10), r.wBW/(128<<10)    //nolint:mnd // typical I/O sizes
			base := time.Duration(300+m.rng.IntN(900)) * time.Microsecond
			if section == "cache" {
				base /= 4
			}
			if v.Name.Display() == slowDevice {
				base *= 6
			}
			r.rWait, r.wWait = base, base*2
		}
		return r, []string{r.String()}
	}
	total := row{name: indent + v.Name.Display()}
	var lines []string
	for _, ch := range v.Children {
		r, ls := m.device(ch, depth+1, section, read, write)
		total.add(r)
		lines = append(lines, ls...)
	}
	return total, append([]string{total.String()}, lines...)
}

// ARC makes up ARC readings: it starts from one recorded on a VM with a
// cache device and walks the hit ratio between 86 and 99.5 percent.
type ARC struct {
	mu    sync.Mutex
	s     zfs.ARCStats
	ratio float64
	rng   *rand.Rand
}

// NewARC returns the demo ARC.
func NewARC() (*ARC, error) {
	b, err := fs.ReadFile(fstui.Recorded, path.Join(fstui.RecordedRoot, "arcstats", "l2arc.txt"))
	if err != nil {
		return nil, fmt.Errorf("demo recording: %w", err)
	}
	s, err := zfs.ParseARCStats(b)
	if err != nil {
		return nil, err
	}
	// Scale the 2 GiB test VM up to a 32 GiB machine.
	const scale = 16
	s.Size, s.Target, s.Min, s.Max = s.Size*scale, s.Target*scale, s.Min*scale, s.Max*scale
	s.Data, s.Metadata, s.MRU, s.MFU = s.Data*scale, s.Metadata*scale, s.MRU*scale, s.MFU*scale
	s.Compressed, s.Uncompressed, s.Memory = s.Compressed*scale, s.Uncompressed*scale, s.Memory*scale
	s.MemoryFree *= scale
	return &ARC{s: s, ratio: 0.95, rng: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 2))}, nil //nolint:gosec // made-up activity
}

// Read is a reading one interval later than the last.
func (a *ARC) Read() (zfs.ARCStats, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ratio = min(max(a.ratio+a.rng.NormFloat64()*0.01, 0.86), 0.995) //nolint:mnd // see the type
	lookups := 2000 + a.rng.Uint64N(4000)                             //nolint:mnd // per second
	hits := uint64(float64(lookups) * a.ratio)
	a.s.Hits += hits
	a.s.IOHits += (lookups - hits) / 10 //nolint:mnd // a few blocks already in flight
	a.s.Misses += lookups - hits - (lookups-hits)/10
	a.s.L2Hits += a.rng.Uint64N(40)    //nolint:mnd // a small cache device
	a.s.L2Misses += a.rng.Uint64N(120) //nolint:mnd // a small cache device
	drift := int64(a.rng.NormFloat64() * 8 * mib)
	a.s.Size = uint64(min(max(int64(a.s.Size)+drift, int64(a.s.Min)), int64(a.s.Max))) //nolint:gosec // within c_min..c_max
	a.s.Target = max(min(a.s.Target, a.s.Max), a.s.Min)
	return a.s, nil
}

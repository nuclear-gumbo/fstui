// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// tuneSource is a fakeSource that also measures, from a recorded phase.
type tuneSource struct {
	*fakeSource
	phase string
}

func (s *tuneSource) ReadTuneSample(pool name.Pool, now time.Time) (zfs.TuneSample, error) {
	root := filepath.Join("../../../testdata/zfs/2.3.9/tune", s.phase)
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
	return r.ReadTuneSample(pool, now)
}

func (*tuneSource) TuneIO(ctx context.Context, _ name.Pool, _ time.Duration, _ func(zfs.IOPool)) error {
	<-ctx.Done()
	return nil
}

func (*tuneSource) Get(context.Context, []string, []string) (map[string]map[string]string, error) {
	return map[string]map[string]string{"fstuitest_tune/small": {"recordsize": "131072"}}, nil
}

// The tab answers from what was measured between two readings, and
// without the tune reads says it is not available.
func TestTuneTab(t *testing.T) {
	ov := golden(t, "healthy")
	src := &tuneSource{fakeSource: &fakeSource{pools: []zfs.Overview{ov}}, phase: "1-seqwrite"}
	p := NewPools(src, defaults())
	p.Update(p.Init()())
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	p.detail.tab = tabTune
	cmd := p.startTune()
	if cmd == nil || p.detail.tune == nil {
		t.Fatal("measuring did not start")
	}
	gen := p.detail.tune.gen
	p.Update(src.sample(t, gen))
	src.phase = "2-smallwrite"
	p.Update(src.sample(t, gen))
	p.detail.tune.records = map[string]uint64{"fstuitest_tune/small": 128 << 10}
	out := ansi.Strip(strings.Join(p.tuneLines(display(), 60), "\n"))
	for _, want := range []string{"fits  Record size against request size", "fstuitest_tune/small: writes average 4.0 KiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if len(l) > 60 {
			t.Errorf("line wider than the tab: %q", l)
		}
	}
	// On another host the files cannot be walked from here: not offered,
	// and not started.
	p.detail.tune.roots = []string{"/tank"}
	p.opts.Remote = true
	if p.measureFiles() != nil {
		t.Error("the per-file walk started for a remote pool")
	}
	for _, b := range p.Keys() {
		if b.Help().Desc == keys.Get(keys.MeasureFiles).Help().Desc {
			t.Error("the per-file walk is offered for a remote pool")
		}
	}
	p.opts.Remote = false
	p.stopTune()

	plain := NewPools(&fakeSource{pools: []zfs.Overview{ov}}, defaults())
	plain.Update(plain.Init()())
	plain.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	plain.detail.tab = tabTune
	if plain.startTune() != nil || !strings.Contains(strings.Join(plain.tuneLines(display(), 60), ""), "not available") {
		t.Error("a source without tune reads")
	}
}

// Moving to another pool with the tab open starts a new measurement; what
// the previous pool's measuring still sends is dropped.
func TestTuneOtherPool(t *testing.T) {
	ov := golden(t, "healthy")
	src := &tuneSource{fakeSource: &fakeSource{pools: []zfs.Overview{ov}}, phase: "1-seqwrite"}
	p := NewPools(src, defaults())
	p.Update(p.Init()())
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	p.detail.tab = tabTune
	p.startTune()
	old := p.detail.tune.gen
	p.stopTune()
	p.detail = &detail{pool: p.detail.pool, tab: tabTune}
	p.startTune()
	if p.detail.tune.gen == old {
		t.Fatalf("the new pool's measurement reuses number %d", old)
	}
	p.Update(src.sample(t, old))
	if !p.detail.tune.win.Start.At.IsZero() {
		t.Error("a sample of the previous pool was taken")
	}
	p.stopTune()
}

func (s *tuneSource) sample(t *testing.T, gen int) tuneSampleMsg {
	t.Helper()
	smp, err := s.ReadTuneSample(name.Pool{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tuneSampleMsg{gen: gen, sample: smp}
}

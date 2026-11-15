// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/uitest"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// now is a week after the golden captures, so ages are stable.
var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

const interval = 5 * time.Second

// golden builds an Overview from the captured output of one scenario
// (testdata/zfs/2.3.9/README.md).
func golden(t *testing.T, scenario string) zfs.Overview {
	t.Helper()
	read := func(f string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", scenario, f))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pools, err := zfs.ParsePoolList(read("zpool-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := zfs.ParsePoolStatus(read("zpool-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	ds, err := zfs.ParseDatasetList(read("zfs-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	o := zfs.Overview{Pool: pools[0], Status: st[0]}
	o.Used, _ = ds[0].Properties.Number("used")
	o.Available, _ = ds[0].Properties.Number("available")
	return o
}

type fakeSource struct {
	pools    []zfs.Overview
	props    map[name.Pool]zfs.Pool
	history  []zfs.HistoryEntry
	datasets []zfs.Dataset
	err      error
	calls    int
}

func (f *fakeSource) Overviews(context.Context) ([]zfs.Overview, error) {
	f.calls++
	return f.pools, f.err
}

func (f *fakeSource) PoolHistory(_ context.Context, pool name.Pool) ([]zfs.HistoryEntry, error) {
	if f.history == nil {
		return nil, errors.New("cannot get history for '" + pool.String() + "': permission denied")
	}
	return f.history, nil
}

func (f *fakeSource) PoolDatasets(_ context.Context, pool name.Pool) ([]zfs.Dataset, error) {
	var out []zfs.Dataset
	for _, d := range f.datasets {
		if d.Pool == pool {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeSource) PoolProperties(_ context.Context, pool name.Pool) (zfs.Pool, error) {
	p, ok := f.props[pool]
	if !ok {
		return zfs.Pool{}, errors.New("no such pool")
	}
	return p, nil
}

// goldenProps reads a scenario's captured zpool get output.
func goldenProps(t *testing.T, scenario string) zfs.Pool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", scenario, "zpool-get-p.json"))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := zfs.ParsePoolGet(b)
	if err != nil {
		t.Fatal(err)
	}
	return ps[0]
}

func defaults() PoolsOptions {
	return PoolsOptions{Interval: interval, Thresholds: config.Default().Thresholds, Now: func() time.Time { return now }}
}

// loaded runs the first load and returns the view with its data.
func loaded(t *testing.T, o PoolsOptions, pools ...zfs.Overview) *Pools {
	t.Helper()
	p := NewPools(&fakeSource{pools: pools}, o)
	msg := p.Init()()
	p.Update(msg)
	return p
}

func display() ui.Display {
	return ui.Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.ASCII, nil), ASCII: true}
}

func render(p *Pools, w, h int) string { return ansi.Strip(p.Render(display(), w, h)) }

func texts(ws []warning) string {
	var b strings.Builder
	for _, w := range ws {
		b.WriteString(w.text + "\n")
	}
	return b.String()
}

func TestOverviewRows(t *testing.T) {
	p := loaded(t, defaults(), golden(t, "healthy"), golden(t, "degraded"), golden(t, "scrub-in-progress"))
	out := render(p, splitWidth-1, 20) // the full-width list; the split narrows it
	for _, want := range []string{"fstuitest_g_healthy", "ONLINE", "DEGRADED", "scrub ", "never", "SCRUB", "FRAG", "DEDUP", "1.00x"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview lacks %q:\n%s", want, out)
		}
	}
	if got := p.Status().Figure; got != "3 pools, 1 DEGRADED" {
		t.Errorf("figure = %q", got)
	}
}

func TestLayoutByWidth(t *testing.T) {
	pools := []zfs.Overview{golden(t, "healthy")}
	narrow := layout(pools, ui.MinWidth)
	if narrow.scrub || narrow.bar > ui.BarMinWidth {
		t.Errorf("80 columns: %+v; want no scrub column and at most the minimum bar", narrow)
	}
	wide := layout(pools, 200)
	if !wide.scrub || !wide.frag || !wide.dedup || wide.bar != ui.BarWidth {
		t.Errorf("200 columns: %+v; want every column and a full bar", wide)
	}
	for _, w := range []int{ui.MinWidth, 120, 200} {
		for _, l := range strings.Split(render(loaded(t, defaults(), pools...), w, 24), "\n") {
			if len([]rune(l)) > w {
				t.Errorf("width %d: line is %d wide: %q", w, len([]rune(l)), l)
			}
		}
	}
}

func TestDegradedIsAlwaysShown(t *testing.T) {
	o := defaults()
	o.Superuser = true
	p := loaded(t, o, golden(t, "degraded"))
	got := texts(p.visibleWarnings())
	if !strings.Contains(got, "fstuitest_g_degraded is DEGRADED: 1 device OFFLINE") {
		t.Fatalf("superuser mode hid a health problem:\n%s", got)
	}
	p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !strings.Contains(texts(p.visibleWarnings()), "DEGRADED") {
		t.Fatal("x hid a health problem")
	}
}

func TestAdvisoryWarningsHideAndReturn(t *testing.T) {
	o := defaults()
	// The healthy capture is a few percent full; thresholds below that
	// make the capacity warning fire.
	o.Thresholds.CapacityWarn, o.Thresholds.CapacityError = 1, 99
	p := loaded(t, o, golden(t, "healthy"))
	if !strings.Contains(texts(p.visibleWarnings()), "full (warning from 1%)") {
		t.Fatalf("no capacity warning:\n%s", texts(p.visibleWarnings()))
	}
	p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if len(p.visibleWarnings()) != 0 || !strings.Contains(p.Status().Figure, "1 warning hidden") {
		t.Fatalf("after x: %q, figure %q", texts(p.visibleWarnings()), p.Status().Figure)
	}
	p.opts.Thresholds.CapacityError = 2 // the pool is now FULL: a new state
	if !strings.Contains(texts(p.visibleWarnings()), "full") {
		t.Fatal("a hidden warning did not return when its state changed")
	}

	o.Superuser = true
	if ws := loaded(t, o, golden(t, "healthy")).visibleWarnings(); len(ws) != 0 {
		t.Fatalf("superuser mode shows advisory warnings:\n%s", texts(ws))
	}
}

func TestScrubOverdueAndCheckpoint(t *testing.T) {
	o := defaults()
	o.Thresholds.ScrubOverdueDays = 1
	got := texts(loaded(t, o, golden(t, "checksum-repaired"), golden(t, "checkpoint")).visibleWarnings())
	for _, want := range []string{
		"fstuitest_g_cksum was last scrubbed 7d ago (overdue after 1 days)",
		// The checkpoint was taken at 17:00 UTC on 13 November: 6d 19h before now.
		"fstuitest_g_ckpt has held a checkpoint for 6d (32.1 MiB); remove, attach, detach, split and reguid are blocked",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings lack %q:\n%s", want, got)
		}
	}
}

func TestVersionMismatch(t *testing.T) {
	o := defaults()
	o.Version = zfs.Version{Userland: "zfs-2.3.9\x1b[2J", Kernel: "zfs-kmod-2.3.8", UserlandRelease: zfs.Release{Major: 2, Minor: 3, Patch: 9}, KernelRelease: zfs.Release{Major: 2, Minor: 3, Patch: 8}}
	got := texts(loaded(t, o).visibleWarnings())
	if !strings.Contains(got, "kernel module zfs-kmod-2.3.8. Reboot or reload") || strings.Contains(got, "\x1b") {
		t.Fatalf("mismatch warning = %q", got)
	}
}

func TestPollingAndBackoff(t *testing.T) {
	src := &fakeSource{err: &execute.CommandError{ExitCode: 1, Stderr: termtext.New("cannot open 'x': permission denied\n")}}
	p := NewPools(src, defaults())
	if p.Init() == nil || p.Init() != nil {
		t.Fatal("a second load started while one was in flight")
	}
	// Doubling per failure from the 5 s interval, capped at 10 intervals.
	for i, want := range []time.Duration{10, 20, 40, 50, 50, 50} {
		p.Update(polled[[]zfs.Overview]{err: src.err, at: now})
		if got := p.poll.delay(); got != want*time.Second {
			t.Errorf("after %d failures: delay %v, want %v", i+1, got, want*time.Second)
		}
	}
	if got := p.Status().LastError.Display(); !strings.Contains(got, "permission denied") {
		t.Errorf("status error = %q, want zpool's stderr", got)
	}
	p.Update(polled[[]zfs.Overview]{at: now})
	if p.poll.delay() != interval || !p.Status().LastError.IsEmpty() {
		t.Error("a success did not reset the backoff and the error")
	}

	stale := p.poll.timer
	p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if _, cmd := p.Update(pollDue[[]zfs.Overview]{timer: stale}); cmd != nil {
		t.Error("a poll superseded by r still started a read")
	}
}

func TestEmptyStates(t *testing.T) {
	if got := render(NewPools(&fakeSource{}, defaults()), 80, 20); !strings.Contains(got, "|Reading pools") {
		t.Errorf("before the first load: %q", got)
	}
	p := NewPools(&fakeSource{}, defaults())
	p.Update(polled[[]zfs.Overview]{err: errors.New("boom"), at: now})
	if got := render(p, 80, 20); !strings.Contains(got, "|Could not read pools") || !strings.Contains(got, "|boom") {
		t.Errorf("first load failed: %q", got)
	}
	if got := render(loaded(t, defaults()), 80, 20); !strings.Contains(got, "|No pools are imported.") {
		t.Errorf("no pools: %q", got)
	}
}

func TestGolden(t *testing.T) {
	pools := []zfs.Overview{golden(t, "healthy"), golden(t, "degraded"), golden(t, "scrub-paused"), golden(t, "checkpoint")}
	for _, sz := range [][2]int{{80, 21}, {120, 37}, {200, 57}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			uitest.Golden(t, loaded(t, defaults(), pools...).Render(display(), sz[0], sz[1]))
		})
	}
}

// Warnings wrap in a narrow list instead of being cut; what does not fit
// in a third of the height is counted.
func TestWarningsWrap(t *testing.T) {
	ws := []warning{
		{text: "fstuitest_demo_backup is DEGRADED: 1 device OFFLINE"},
		{text: "fstuitest_demo_tank is 85% full (warning from 80%)", advisory: true},
		{text: "third", advisory: true},
	}
	got := ansi.Strip(strings.Join(warningBlock(display(), ws, 30, 9), "\n")) // three lines for warnings
	want := "\nfstuitest_demo_backup is\n  DEGRADED: 1 device OFFLINE\n+2 more"
	if got != want {
		t.Errorf("at 30x9:\n%q\nwant\n%q", got, want)
	}
	got = ansi.Strip(strings.Join(warningBlock(display(), ws, 60, 30), "\n"))
	if !strings.Contains(got, "(warning from 80%)") || !strings.Contains(got, "third") || strings.Contains(got, "more") {
		t.Errorf("at 60x30:\n%s", got)
	}
}

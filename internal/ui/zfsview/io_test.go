// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// goldenIOSamples parses the captured zpool iostat output.
func goldenIOSamples(t *testing.T) []zfs.IOSample {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", "iostat", "four-pools.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out []zfs.IOSample
	p := zfs.IOStatParser{Sample: func(s zfs.IOSample) { out = append(out, s) }}
	for _, l := range strings.Split(string(b), "\n") {
		if err := p.Line(l); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// fakeIO hands over its samples, then runs until cancelled, as zpool
// iostat would; err, if set, ends the stream at once instead.
type fakeIO struct {
	samples   []zfs.IOSample
	err       error
	cancelled chan struct{}
}

func (f *fakeIO) IOStats(ctx context.Context, sample func(zfs.IOSample)) error {
	if f.err != nil {
		return f.err
	}
	for _, s := range f.samples {
		sample(s)
	}
	<-ctx.Done()
	close(f.cancelled)
	return nil
}

func newIO(t *testing.T, src *fakeIO) (*IO, *time.Time) {
	t.Helper()
	clock := now
	return NewIO(src, nil, func() time.Time { clock = clock.Add(time.Second); return clock }), &clock
}

// pump runs n messages of the stream through the view.
func pump(v *IO, cmd tea.Cmd, n int) {
	for range n {
		_, cmd = v.Update(cmd())
	}
}

// zpool iostat runs from start to quit, shown or not, so the graphs have
// no gaps.
func TestIOStreamsFromStart(t *testing.T) {
	src := &fakeIO{samples: goldenIOSamples(t), cancelled: make(chan struct{})}
	v, _ := newIO(t, src)
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("zpool iostat did not start with fstui")
	}
	pump(v, cmd, 3)
	if v.Activate() != nil {
		t.Fatal("showing the view started a second zpool iostat")
	}
	if len(v.latest) != 4 || v.at.IsZero() {
		t.Fatalf("after three samples: %d pools", len(v.latest))
	}
	v.cursor.i = 1 // fstuitest_demo_roles
	out := ansi.Strip(v.Render(display(), 140, 30))
	for _, want := range []string{
		"POOL", "READ", "WRITE", "PEAK", "fstuitest_demo_scratch", "fstuitest_demo_roles devices, last second",
		"raidz1-0", "special", "logs", "cache", "READ WAIT", "/s",
		"fstuitest_demo_roles, last 3 s", "read ", "write ", "peak ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("I/O view lacks %q:\n%s", want, out)
		}
	}
	v.stop()
	select {
	case <-src.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping did not stop zpool iostat")
	}
	// A sample from the stopped stream is ignored.
	before := v.at
	v.Update(ioSampleMsg{gen: v.gen - 1, sample: src.samples[0], at: before.Add(time.Hour)})
	if v.at != before {
		t.Error("a stopped stream's sample was recorded")
	}
}

func TestIOStreamFails(t *testing.T) {
	v, _ := newIO(t, &fakeIO{err: errors.New("no pools available")})
	cmd := v.Activate()
	v.Update(cmd())
	out := ansi.Strip(v.Render(display(), 100, 20))
	if !strings.Contains(out, "zpool iostat stopped") || !strings.Contains(out, "no pools available") || v.running {
		t.Errorf("failed stream:\n%s", out)
	}
	if _, cmd := v.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd == nil {
		t.Error("r did not restart zpool iostat")
	}
}

// Pools that disappear from the stream lose their graphs.
func TestIOForgetsExportedPools(t *testing.T) {
	samples := goldenIOSamples(t)
	v, clock := newIO(t, &fakeIO{})
	v.record(samples[0], *clock)
	v.record(samples[1][:2], clock.Add(time.Second))
	if len(v.read) != 2 || v.read["fstuitest_demo_tank"] != nil {
		t.Errorf("series kept for %d pools", len(v.read))
	}
}

func TestWaitFormat(t *testing.T) {
	d := display()
	for _, c := range []struct {
		t    time.Duration
		ok   bool
		want string
	}{
		{0, false, "-"}, {196608 * time.Nanosecond, true, "196 us"}, {6042696 * time.Nanosecond, true, "6.0 ms"}, {1500 * time.Millisecond, true, "1.50 s"},
	} {
		if got := wait(d, c.t, c.ok); got != c.want {
			t.Errorf("wait(%v) = %q, want %q", c.t, got, c.want)
		}
	}
	if got := wait(ui.Display{}, time.Microsecond, true); got != "1 µs" {
		t.Errorf("Unicode micro: %q", got)
	}
}

func TestAgo(t *testing.T) {
	for span, want := range map[time.Duration]string{
		45 * time.Second: "45 s", 2 * time.Minute: "2 min", 150 * time.Second: "2 min 30 s",
	} {
		if got := ago(span); got != want {
			t.Errorf("ago(%s) = %q, want %q", span, got, want)
		}
	}
}

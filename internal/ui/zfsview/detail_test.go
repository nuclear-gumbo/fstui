// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/uitest"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// opened loads the scenarios and opens the first pool, with its
// properties read.
func opened(t *testing.T, scenarios ...string) *Pools {
	t.Helper()
	src := &fakeSource{props: map[name.Pool]zfs.Pool{}}
	for _, s := range scenarios {
		o := golden(t, s)
		src.pools = append(src.pools, o)
		src.props[o.Pool.Name] = goldenProps(t, s)
		src.datasets = append(src.datasets, goldenDatasets(t, s)...)
	}
	p := NewPools(src, defaults())
	p.Update(p.Init()())
	pressKey(p, "enter")
	if !p.focus || p.detail == nil {
		t.Fatal("enter did not open the pool")
	}
	return p
}

// toTab presses ] until the pool detail shows tab.
func toTab(p *Pools, tab int) {
	for p.detail.tab != tab {
		pressKey(p, "l")
	}
}

// pressKey sends a key and runs the read it starts, as the runtime
// would; key presses start reads, never timers.
func pressKey(p *Pools, k string) {
	msg := tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	switch k {
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	_, cmd := p.Update(msg)
	run(p, cmd)
}

// run executes cmd and feeds its messages back, batches included.
func run(p *Pools, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch m := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range m {
			run(p, c)
		}
	case nil:
	default:
		p.Update(m)
	}
}

func TestOpenAndClose(t *testing.T) {
	p := opened(t, "roles")
	if !p.Nested() || p.detail.pool.String() != "fstuitest_g_roles" {
		t.Fatalf("after enter: nested %v", p.Nested())
	}
	pressKey(p, "esc")
	if p.Nested() || p.focus {
		t.Fatal("esc did not return to the overview")
	}
}

func TestTopologyRoles(t *testing.T) {
	out := render(opened(t, "roles"), 120, 40)
	for _, want := range []string{"Topology", "raidz1-0", "special", "mirror-1", "logs", "cache", "spares", "AVAIL", "fstuitest_g_roles-spare.img"} {
		if !strings.Contains(out, want) {
			t.Errorf("topology lacks %q:\n%s", want, out)
		}
	}
}

func TestTopologyProblemsCheckpointAndScan(t *testing.T) {
	if out := render(opened(t, "degraded"), 120, 30); !strings.Contains(out, "taken offline") || !strings.Contains(out, "OFFLINE") {
		t.Errorf("degraded pool: explanation or OFFLINE member missing:\n%s", out)
	}
	if out := render(opened(t, "checkpoint"), 110, 30); !strings.Contains(out, "holds 32.1 MiB") || !strings.Contains(out, "reguid are blocked") {
		t.Errorf("checkpoint line missing:\n%s", out)
	}
	if out := render(opened(t, "checksum-repaired"), 120, 30); !strings.Contains(out, "Scrub finished 7d ago") || !strings.Contains(out, "771") {
		t.Errorf("finished scrub or checksum count missing:\n%s", out)
	}
	if out := render(opened(t, "scrub-paused"), 120, 30); !strings.Contains(out, "Scrub paused") {
		t.Errorf("paused scrub missing:\n%s", out)
	}
}

func TestScanEstimate(t *testing.T) {
	start := now.Add(-10 * time.Minute)
	sc := zfs.Scan{Function: "SCRUB", State: "SCANNING", Start: start, ToExamine: 400, Issued: 100}
	// A quarter done in 10 minutes: 30 minutes left at the same rate.
	if got := scanLine(sc, now, false); !strings.Contains(got, "about 30m left") {
		t.Errorf("scan line = %q", got)
	}
	sc.Issued = 450 // past 100%: no estimate (zpool-scrub(8))
	if got := scanLine(sc, now, false); strings.Contains(got, "left") {
		t.Errorf("estimate given past 100%%: %q", got)
	}
}

func TestPropertiesAndFeatures(t *testing.T) {
	p := opened(t, "healthy")
	toTab(p, tabProperties)
	out := render(p, 110, 60)
	for _, want := range []string{"PROPERTY", "ashift", "cachefile", "failmode", "wait", "default", "480 MiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("properties lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "feature@") || strings.Contains(out, "zpool_checkpoint") {
		t.Error("features listed on the properties tab")
	}
	pressKey(p, "l")
	if out := render(p, 110, 60); !strings.Contains(out, "FEATURE") || !strings.Contains(out, "allocation_classes") || !strings.Contains(out, "Pool feature flag") {
		t.Errorf("features tab:\n%s", out)
	}
	pressKey(p, "h")
	pressKey(p, "h")
	pressKey(p, "h")
	if p.detail.tab != tabTopology {
		t.Error("[ did not cycle back to the topology tab")
	}
}

func TestStalePropertiesIgnored(t *testing.T) {
	p := opened(t, "healthy", "roles")
	toTab(p, tabProperties) // properties are read when their tab is shown
	other := golden(t, "roles").Pool.Name
	p.Update(propsMsg{pool: other, props: goldenProps(t, "roles"), at: now})
	if p.detail.props.Name == other {
		t.Fatal("a reply for another pool replaced the open pool's properties")
	}
}

func TestOpenPoolDisappears(t *testing.T) {
	p := opened(t, "healthy")
	p.Update(polled[[]zfs.Overview]{at: now})
	if out := render(p, 110, 30); !strings.Contains(out, "is no longer imported") {
		t.Errorf("vanished pool: %q", out)
	}
}

func TestDetailGolden(t *testing.T) {
	t.Run("roles_topology_120x30", func(t *testing.T) {
		uitest.Golden(t, opened(t, "roles").Render(display(), 120, 30))
	})
	t.Run("healthy_properties_80x21", func(t *testing.T) {
		p := opened(t, "healthy")
		toTab(p, tabProperties)
		uitest.Golden(t, p.Render(display(), 80, 21))
	})
}

func TestHistoryTab(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", "healthy", "zpool-history.txt"))
	if err != nil {
		t.Fatal(err)
	}
	es, err := zfs.ParseHistory(b)
	if err != nil {
		t.Fatal(err)
	}
	o := defaults()
	o.Root = true
	ov := golden(t, "healthy")
	src := &fakeSource{pools: []zfs.Overview{ov}, props: map[name.Pool]zfs.Pool{ov.Pool.Name: goldenProps(t, "healthy")}, history: es}
	p := NewPools(src, o)
	p.Update(p.Init()())
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"}) // topology wraps to history
	p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"}) // features
	if p.detail.tab != tabFeatures {
		t.Fatalf("tab %d", p.detail.tab)
	}
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if p.detail.tab != tabHistory || cmd == nil {
		t.Fatal("entering the history tab did not start a read")
	}
	p.Update(cmd())
	out := render(p, 110, 60)
	if strings.Index(out, "zfs unload-key") > strings.Index(out, "zpool create") || strings.Contains(out, "[txg") || strings.Contains(out, "ioctl") {
		t.Fatalf("want user commands only, newest first:\n%s", out)
	}
	if strings.ContainsAny(out, "\x1b\x07\r") || !strings.Contains(out, `com.example:note=evil\x1b[2J`) {
		t.Fatal("hostile property value not shown with visible escapes")
	}
	p.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if out := render(p, 110, 200); !strings.Contains(out, "ioctl create") || !strings.Contains(out, "create pool version 5000") {
		t.Fatalf("i did not show internal events:\n%s", out)
	}
}

func TestHistoryNeedsRoot(t *testing.T) {
	p := opened(t, "healthy") // defaults: not root
	for range tabHistory {
		p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	}
	if out := render(p, 110, 20); !strings.Contains(out, "History needs root") {
		t.Fatalf("unprivileged history tab:\n%s", out)
	}
}

func TestPropertyHelpFollowsSelection(t *testing.T) {
	p := opened(t, "healthy")
	toTab(p, tabProperties)
	out := render(p, 110, 30)
	if !strings.Contains(out, "> allocated") || !strings.Contains(out, "Reported by ZFS; it cannot be set.") {
		t.Fatalf("first property and its help:\n%s", out)
	}
	for range 3 { // allocated, altroot, ashift, autoexpand
		pressKey(p, "j")
	}
	out = render(p, 110, 30)
	if !strings.Contains(out, "> autoexpand") || !strings.Contains(out, "Grow the pool automatically") || !strings.Contains(out, "Values: on | off") {
		t.Fatalf("help did not follow the selection:\n%s", out)
	}
	if small := render(p, 110, 12); strings.Contains(small, "Values:") {
		t.Errorf("help shown in a 12-line view, leaving too few rows:\n%s", small)
	}
}

// goldenDatasets reads a scenario's filesystems and volumes from its
// captured zfs get all.
func goldenDatasets(t *testing.T, scenario string) []zfs.Dataset {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", scenario, "zfs-get-p.json"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := zfs.ParseDatasetGet(b)
	if err != nil {
		t.Fatal(err)
	}
	var out []zfs.Dataset
	for _, d := range all {
		if d.Type == zfs.Filesystem || d.Type == zfs.Volume {
			out = append(out, d)
		}
	}
	return out
}

func TestSpaceTab(t *testing.T) {
	p := opened(t, "healthy")
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if p.detail.tab != tabSpace || cmd == nil {
		t.Fatal("entering the Space tab did not read the pool's datasets")
	}
	if _, again := p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"}); again != nil {
		t.Fatal("leaving the Space tab started a read")
	}
	p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	p.Update(cmd())
	out := render(p, 110, 30)
	for _, want := range []string{"fstuitest_g_healthy  ", "% full", "freeing", "data  = snapshots  . reservation"} {
		if !strings.Contains(out, want) {
			t.Errorf("Capacity lacks %q:\n%s", want, out)
		}
	}
	// The capture writes 16 MiB into home/user and 8 MiB into home, so
	// home/user holds the most on its own and fills its gauge.
	user, home := strings.Index(out, "|fstuitest_g_healthy/home/user "), strings.Index(out, "|fstuitest_g_healthy/home ")
	if user < 0 || home < 0 || user > home {
		t.Errorf("ranking:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "home/user ") && !strings.Contains(l, strings.Repeat("#", 20)) {
			t.Errorf("the largest row should fill most of its gauge: %q", l)
		}
	}
}

// Help wraps to the width instead of running off the edge.
func TestFeatureHelpWraps(t *testing.T) {
	p := opened(t, "healthy")
	toTab(p, tabFeatures)
	out := render(p, 80, 40) // the help is about 150 cells long
	for _, l := range strings.Split(out, "\n") {
		if w := len([]rune(l)); w > 80 {
			t.Errorf("line of %d cells in an 80-column view: %q", w, l)
		}
	}
	if !strings.Contains(out, "zpool-features(7) describes") || !strings.Contains(out, "allocation_classes.") {
		t.Errorf("wrapped help incomplete:\n%s", out)
	}
}

func TestMouseInPools(t *testing.T) {
	p := opened(t, "healthy", "roles")
	pressKey(p, "esc")
	render(p, 110, 30) // records the first visible row
	p.Update(ui.ScrollMsg{Delta: 1})
	if p.cursor != 1 {
		t.Fatalf("wheel down: cursor %d", p.cursor)
	}
	p.Update(ui.ClickMsg{X: 4, Y: listTop}) // first pool row, below the edge and header
	if p.cursor != 0 || p.detail != nil {
		t.Fatalf("click: cursor %d, open %v", p.cursor, p.detail != nil)
	}
	p.Update(ui.ClickMsg{X: 4, Y: listTop})
	if p.detail == nil || !p.focus {
		t.Fatal("a second click on the selected pool did not open it")
	}
	render(p, 110, 30)
	top := strings.Split(render(p, 110, 30), "\n")[0]
	p.Update(ui.ClickMsg{X: strings.Index(top, "Capacity"), Y: 0})
	if p.detail.tab != tabSpace {
		t.Fatalf("click on Capacity in %q selected tab %d", top, p.detail.tab)
	}
	p.Update(ui.ClickMsg{X: strings.Index(top, "Properties"), Y: 1}) // below the edge: not a tab
	if p.detail.tab != tabSpace {
		t.Error("a click below the top edge switched tabs")
	}
}

// Properties read with -p get back the units zfs get would print:
// percentages, ratios, dates, and "none" for a zero quota.
func TestPropertyValues(t *testing.T) {
	pool := goldenProps(t, "healthy").Properties
	root := goldenDatasets(t, "healthy")[0].Properties
	d := display()
	created, _ := root.Number("creation")
	for _, c := range []struct {
		n     string
		v     zfs.Property
		bytes map[string]bool
		want  string
	}{
		{"capacity", pool["capacity"], byteProperties, "5%"},
		{"fragmentation", pool["fragmentation"], byteProperties, "3%"},
		{"dedupratio", pool["dedupratio"], byteProperties, "1.00x"},
		{"size", pool["size"], byteProperties, "480 MiB"},
		{"compressratio", root["compressratio"], datasetByteProperties, "1.00x"},
		{"quota", root["quota"], datasetByteProperties, "none"},
		{"creation", root["creation"], datasetByteProperties, time.Unix(int64(created), 0).Local().Format("2006-01-02 15:04")},
		{"recordsize", root["recordsize"], datasetByteProperties, "128 KiB"},
	} {
		if got := propertyValue(d, c.n, c.v, c.bytes); got != c.want {
			t.Errorf("%s = %q, want %q", c.n, got, c.want)
		}
	}
}

// The summary names the last scan, which may be a resilver, not "scrub"
// before it ("scrub resilver 0%" is wrong).
func TestSummaryNamesTheScan(t *testing.T) {
	cases := map[string]string{"resilver-in-progress": "resilver 0%", "healthy": "scrub never", "scrub-paused": "scrub paused "}
	for scenario, want := range cases {
		out := render(opened(t, scenario), 110, 30)
		if !strings.Contains(out, want) || strings.Contains(out, "scrub resilver") {
			t.Errorf("%s: summary lacks %q:\n%s", scenario, want, strings.Join(strings.Split(out, "\n")[:4], "\n"))
		}
	}
	o := golden(t, "resilver-in-progress")
	if got := scrubText(o, func() time.Time { return now }); got != "resilver 0%" {
		t.Errorf("overview column = %q", got)
	}
}

func TestExpandLine(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	st := zfs.PoolStatus{
		Data:      zfs.Vdev{Children: []zfs.Vdev{{Name: termtext.New("raidz1-0")}}},
		Expansion: &zfs.Expansion{State: "SCANNING", Start: now.Add(-10 * time.Minute), ToReflow: 4 << 30, Reflowed: 1 << 30},
	}
	if got := expandLine(st, now, false); !strings.Contains(got, "Expanding raidz1-0: 1.0 GiB of 4.0 GiB copied") || !strings.Contains(got, "about 30m left") {
		t.Errorf("running: %q", got)
	}
	st.Expansion.State, st.Expansion.End, st.Expansion.Reflowed = "FINISHED", now.Add(-time.Hour), 4<<30
	if got := expandLine(st, now, false); !strings.HasPrefix(got, "Expanded raidz1-0 1h ago") {
		t.Errorf("finished: %q", got)
	}
}

func TestRemoveLine(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	st := zfs.PoolStatus{
		Data:    zfs.Vdev{Children: []zfs.Vdev{{Name: termtext.New("mirror-0")}, {Name: termtext.New("mirror-1")}}},
		Removal: &zfs.Removal{Vdev: 1, State: "SCANNING", Start: now.Add(-10 * time.Minute), ToCopy: 2 << 30, Copied: 1 << 30},
	}
	if got := removeLine(st, now, false); !strings.Contains(got, "Removing mirror-1: 1.0 GiB of 2.0 GiB copied") || !strings.Contains(got, "about 10m left") {
		t.Errorf("running: %q", got)
	}
	st.Removal.State, st.Removal.End = "FINISHED", now.Add(-time.Hour)
	if got := removeLine(st, now, false); !strings.HasPrefix(got, "Removed vdev 1 1h ago") {
		t.Errorf("finished: %q", got)
	}
}

// A scan without a function name (not seen from zpool, but not ruled
// out) still reads as a sentence.
func TestScanLineWithoutFunction(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if got := scanLine(zfs.Scan{State: "FINISHED", Start: now.Add(-time.Hour), End: now}, now, false); !strings.HasPrefix(got, "Scan finished") {
		t.Errorf("%q", got)
	}
}

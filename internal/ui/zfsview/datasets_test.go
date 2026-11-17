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

	"github.com/nuclear-gumbo/fstui/internal/ui/uitest"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// fakeDatasets serves the healthy capture: zfs get all for every dataset
// stands in for both the listing and the per-dataset reads.
type fakeDatasets struct {
	all   []zfs.Dataset
	allow []zfs.Delegation
}

func (f *fakeDatasets) Allow(context.Context, name.Dataset) ([]zfs.Delegation, error) {
	return f.allow, nil
}

func (f *fakeDatasets) Datasets(context.Context) ([]zfs.Dataset, error) {
	var out []zfs.Dataset
	for _, d := range f.all {
		if d.Type == zfs.Filesystem || d.Type == zfs.Volume {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeDatasets) DatasetProperties(_ context.Context, ds name.Dataset) (zfs.Dataset, error) {
	for _, d := range f.all {
		if d.Name == ds.String() {
			return d, nil
		}
	}
	return zfs.Dataset{}, errors.New("dataset does not exist")
}

func (f *fakeDatasets) DatasetSnapshots(_ context.Context, ds name.Dataset) ([]zfs.SnapshotRow, error) {
	var out []zfs.SnapshotRow
	for _, d := range f.all {
		if d.Type == zfs.Snapshot && strings.HasPrefix(d.Name, ds.String()+"@") {
			out = append(out, snapRow(d))
		}
	}
	return out, nil
}

// snapRow is a recorded snapshot as the snapshot lists read it.
func snapRow(d zfs.Dataset) zfs.SnapshotRow {
	n := func(p string) uint64 { v, _ := d.Properties.Number(p); return v }
	s := zfs.SnapshotRow{Name: d.Name, CreateTXG: d.CreateTXG, Creation: n("creation"), Used: n("used"), Referenced: n("referenced"), Holds: n("userrefs")}
	if c := d.Properties["clones"].Text; c.Raw() != "-" {
		s.Clones = c
	}
	return s
}

func healthyDatasets(t *testing.T) *fakeDatasets {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "zfs", "2.3.9", "healthy", "zfs-get-p.json"))
	if err != nil {
		t.Fatal(err)
	}
	all, err := zfs.ParseDatasetGet(b)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeDatasets{all: all}
}

func loadedDatasets(t *testing.T) *Datasets {
	t.Helper()
	v := NewDatasets(healthyDatasets(t), DatasetsOptions{Interval: interval, Now: func() time.Time { return now }})
	v.Update(v.Init()())
	return v
}

func names(rows []*node) []string {
	out := make([]string, len(rows))
	for i, n := range rows {
		out[i] = strings.Repeat(" ", n.depth) + n.d.Name
	}
	return out
}

func TestTreeKeepsChildrenUnderParents(t *testing.T) {
	ds := func(n string) zfs.Dataset { return zfs.Dataset{Name: n} }
	// zfs list order: "t/a-b" sorts between "t/a" and "t/a/c".
	v := &Datasets{collapsed: map[string]bool{}, poll: poller[[]zfs.Dataset]{data: []zfs.Dataset{ds("t"), ds("t/a"), ds("t/a-b"), ds("t/a/c")}}}
	got := strings.Join(names(v.rows()), ",")
	if want := "t, t/a,  t/a/c, t/a-b"; got != want {
		t.Fatalf("tree = %q, want %q", got, want)
	}
}

func kp(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	return tea.KeyPressMsg{Code: rune(k[0]), Text: k}
}

func TestCollapseExpandAndSelection(t *testing.T) {
	v := loadedDatasets(t)
	for _, k := range []string{"j", "j"} { // root, clone, home
		v.Update(kp(k))
	}
	if v.selected != "fstuitest_g_healthy/home" {
		t.Fatalf("selected %q", v.selected)
	}
	v.Update(kp("h"))
	if strings.Contains(strings.Join(names(v.rows()), ","), "home/user") {
		t.Fatal("h did not collapse home")
	}
	v.Update(kp("h")) // already collapsed: move to the parent
	if v.selected != "fstuitest_g_healthy" {
		t.Fatalf("h on a collapsed node selected %q, want its parent", v.selected)
	}
	v.Update(kp("j"))
	v.Update(kp("j"))
	v.Update(kp("l"))
	if !strings.Contains(strings.Join(names(v.rows()), ","), "home/user") {
		t.Fatal("l did not expand home")
	}
	v.Update(v.poll.refresh()()) // a refresh keeps the selection by name
	if v.selected != "fstuitest_g_healthy/home" {
		t.Fatalf("selection after refresh: %q", v.selected)
	}
}

func TestDatasetList(t *testing.T) {
	v := loadedDatasets(t)
	wide := render2(v, 24)
	for _, want := range []string{"AVAIL", "RATIO", "locked", "v fstuitest_g_healthy"} {
		if !strings.Contains(wide, want) {
			t.Errorf("110 columns lack %q:\n%s", want, wide)
		}
	}
	if got := v.Status().Figure; got != "7 datasets in 1 pool" {
		t.Errorf("figure = %q", got)
	}
}

// render2 draws Datasets below splitWidth, where one panel fills the view.
func render2(v *Datasets, h int) string { return ansi.Strip(v.Render(display(), 110, h)) }

// openHome opens fstuitest_g_healthy/home, which has a child, snapshots,
// holds and a clone.
func openHome(t *testing.T, v *Datasets) {
	t.Helper()
	v.selected = "fstuitest_g_healthy/home"
	_, cmd := v.Update(kp("enter"))
	if cmd == nil {
		t.Fatal("enter started no reads")
	}
	for _, m := range batch(cmd) {
		v.Update(m)
	}
}

// batch runs a tea.Batch and returns its messages.
func batch(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	b, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range b {
		if c != nil {
			out = append(out, c())
		}
	}
	return out
}

func TestDatasetSpace(t *testing.T) {
	v := loadedDatasets(t)
	openHome(t, v)
	unicode := display()
	unicode.ASCII = false
	out := ansi.Strip(v.Render(unicode, 110, 30))
	for _, want := range []string{"Capacity", "usedbysnapshots: freed by destroying every snapshot", "children", "written", "compression"} {
		if !strings.Contains(out, want) {
			t.Errorf("space tab lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "█") || !strings.Contains(out, "▒") {
		t.Errorf("gauge lacks data or children cells:\n%s", out)
	}
}

func TestDatasetPropertiesAndSnapshots(t *testing.T) {
	v := loadedDatasets(t)
	openHome(t, v)
	v.Update(kp("l"))
	props := render2(v, 120)
	if !strings.Contains(props, "inherited from fstuitest_g_healthy") || !strings.Contains(props, "com.example:note") {
		t.Errorf("properties:\n%s", props)
	}
	if strings.Contains(props, "\x1b[2J") {
		t.Error("hostile user property reached the terminal raw")
	}
	v.Update(kp("l"))
	snaps := render2(v, 30)
	for _, want := range []string{"2 snapshots, sorted by created", "@daily-1", "@daily-2", "fstuitest_g_healthy/clone"} {
		if !strings.Contains(snaps, want) {
			t.Errorf("snapshots lack %q:\n%s", want, snaps)
		}
	}
	v.Update(kp("S"))
	out := render2(v, 30)
	if strings.Index(out, "@daily-2") > strings.Index(out, "@daily-1") {
		t.Errorf("S did not reverse the order:\n%s", out)
	}
}

func TestDatasetDetailDropsOtherDatasets(t *testing.T) {
	v := loadedDatasets(t)
	openHome(t, v)
	v.Update(polled[dsProps]{data: dsProps{ds: "fstuitest_g_healthy/vol"}})
	if v.detail.props.data.ds != "fstuitest_g_healthy/home" {
		t.Fatal("properties of another dataset replaced the open one's")
	}
	v.Update(kp("esc"))
	if v.Nested() {
		t.Fatal("esc did not close the dataset")
	}
}

func TestDatasetGolden(t *testing.T) {
	v := loadedDatasets(t)
	t.Run("list_120x16", func(t *testing.T) { uitest.Golden(t, v.Render(display(), 120, 16)) })
	openHome(t, v)
	t.Run("space_100x16", func(t *testing.T) { uitest.Golden(t, v.Render(display(), 100, 16)) })
}

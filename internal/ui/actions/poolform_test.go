// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

func poolDevices() []disks.Device {
	return []disks.Device{
		{Name: "sda", Path: "/dev/sda", ByID: "/dev/disk/by-id/ata-A", Size: 4 << 40, Type: "disk"},
		{Name: "sdb", Path: "/dev/sdb", ByID: "/dev/disk/by-id/ata-B", Size: 4 << 40, Type: "disk"},
		{Name: "sdc", Path: "/dev/sdc", ByID: "/dev/disk/by-id/ata-C", Size: 4 << 40, Type: "disk", FSType: "zfs_member"},
		{Name: "sdd", Path: "/dev/sdd", ByID: "/dev/disk/by-id/ata-D", Size: 1 << 40, Type: "disk", Mountpoints: []string{"/"}},
		{Name: "sr0", Path: "/dev/sr0", Size: 1 << 30, Type: "rom"},
	}
}

func openedPoolForm(t *testing.T) *poolForm {
	t.Helper()
	b := plan.Builder{Devices: func(context.Context) ([]disks.Device, error) { return poolDevices(), nil }}
	m, cmd := openPoolForm(b, "", func(*mutate.Plan, Rebuild) ui.Modal { return nil })
	f, ok := m.(*poolForm)
	if !ok {
		t.Fatalf("opened %T", m)
	}
	f.Update(cmd())
	return f
}

func pressPool(f *poolForm, keys ...string) {
	for _, k := range keys {
		switch k {
		case "space":
			f.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		case "tab":
			f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		default:
			for _, r := range k {
				f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
		}
	}
}

// Usable devices get a role each; refused ones are listed apart, and a
// CD drive not at all.
func TestPoolFormSortsDevices(t *testing.T) {
	f := openedPoolForm(t)
	if len(f.usable) != 3 || len(f.refused) != 1 || f.refused[0].Name != "sdd" {
		t.Fatalf("usable %d, refused %v", len(f.usable), f.refused)
	}
	out := render(f)
	for _, want := range []string{"Name*", "Layout: mirror", "ata-C", "holds zfs_member", "Not usable", "ata-D  mounted at /"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sr0") {
		t.Error("the CD drive is listed")
	}
}

// Space sets each device's role; the spec follows the form.
func TestPoolFormSpec(t *testing.T) {
	f := openedPoolForm(t)
	pressPool(f, "tank", "tab", "tab", "space", "tab", "space", "tab", "space", "space")
	s := f.spec()
	if s.Name != "tank" || s.Layout != "mirror" || len(s.Data) != 2 || len(s.Log) != 1 || s.Log[0] != "/dev/disk/by-id/ata-C" {
		t.Errorf("spec %+v", s)
	}
}

func TestPoolFormNeedsNameAndData(t *testing.T) {
	f := openedPoolForm(t)
	if cmd := f.build(); cmd != nil || !strings.Contains(f.err, "Name is required") {
		t.Errorf("no name: %q", f.err)
	}
	f.name = "tank"
	if cmd := f.build(); cmd != nil || !strings.Contains(f.err, "at least one data device") {
		t.Errorf("no devices: %q", f.err)
	}
}

// Without lsblk the form says why instead of listing nothing.
func TestPoolFormWithoutInventory(t *testing.T) {
	m, cmd := openPoolForm(plan.Builder{}, "", nil)
	m.Update(cmd())
	if out := render(m); !strings.Contains(out, "needs lsblk") {
		t.Errorf("without lsblk:\n%s", out)
	}
}

// Adding to a pool: no name or mount options, a checkpoint switch, and
// the layout starts as the pool's own.
func TestPoolFormAdd(t *testing.T) {
	st := zfs.PoolStatus{Data: zfs.Vdev{Children: []zfs.Vdev{{Name: termtext.New("raidz2-0"), Type: "raidz", Children: []zfs.Vdev{{}, {}, {}, {}}}}}}
	b := plan.Builder{
		R:       topoReader{st: st},
		Devices: func(context.Context) ([]disks.Device, error) { return poolDevices(), nil },
	}
	m, cmd := openPoolForm(b, "tank", nil)
	f, ok := m.(*poolForm)
	if !ok {
		t.Fatalf("opened %T", m)
	}
	f.Update(cmd())
	out := render(f)
	for _, want := range []string{"Add devices to tank", "Layout: raidz2", "checkpoint first"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Name*") || strings.Contains(out, "Mountpoint") {
		t.Errorf("create fields shown:\n%s", out)
	}
	if f.build(); !strings.Contains(f.err, "at least one device") {
		t.Errorf("empty: %q", f.err)
	}
}

// topoReader answers PoolTopology; nothing else is asked of it.
type topoReader struct {
	plan.Reader
	st zfs.PoolStatus
}

func (r topoReader) PoolTopology(context.Context, string) (zfs.PoolStatus, error) { return r.st, nil }

// importReader lists one importable pool.
type importReader struct{ plan.Reader }

func (importReader) Importable(context.Context, []string) ([]zfs.Importable, error) {
	return []zfs.Importable{{
		Name: termtext.New("oldtank"), ID: "48", State: "DEGRADED",
		Status:  []termtext.Text{termtext.New("One or more devices are missing from the system.")},
		Devices: []zfs.ImportDevice{{Name: termtext.New("oldtank"), State: "DEGRADED"}, {Depth: 1, Name: termtext.New("sdb"), State: "UNAVAIL", Note: termtext.New("cannot open")}},
	}}, nil
}

func TestImportForm(t *testing.T) {
	m, cmd := openImportForm(plan.Builder{R: importReader{}}, nil)
	f, ok := m.(*importForm)
	if !ok {
		t.Fatalf("opened %T", m)
	}
	f.Update(cmd())
	out := render(f)
	for _, want := range []string{"> oldtank  DEGRADED  id 48", "devices are missing", "sdb  UNAVAIL  cannot open", "Read-only"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	f.focus = 1 + len(f.found) + importReadOnly
	f.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if s := f.spec(); s.ID != "48" || !s.ReadOnly || s.Dirs != nil {
		t.Errorf("spec %+v", s)
	}
}

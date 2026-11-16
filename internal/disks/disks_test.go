// SPDX-License-Identifier: GPL-3.0-or-later

package disks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// testdata/linux/lsblk.json: the dev VM, with a spare disk (sda), the
// system disk (sdb: / on sdb1, swap on sdb5, an extended partition sdb2)
// and a CD-ROM.
func recorded(t *testing.T) []Device {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "linux", "lsblk.json"))
	if err != nil {
		t.Fatal(err)
	}
	ds, err := ParseLsblk(b)
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func find(ds []Device, name string) Device {
	for _, d := range ds {
		if d.Name == name {
			return d
		}
	}
	return Device{}
}

func TestParseLsblk(t *testing.T) {
	ds := recorded(t)
	if sdb1 := find(ds, "sdb1"); sdb1.Parent != "sdb" || sdb1.FSType != "ext4" || len(sdb1.Mountpoints) != 1 || sdb1.Mountpoints[0] != "/" {
		t.Errorf("sdb1 = %+v", sdb1)
	}
	if sda := find(ds, "sda"); sda.Size != 21474836480 || sda.Type != "disk" || len(sda.Mountpoints) != 0 {
		t.Errorf("sda = %+v", sda)
	}
}

// Every reason Judge gives, from the recorded machine and made-up devices.
func TestJudge(t *testing.T) {
	ds := recorded(t)
	for _, c := range []struct {
		dev  Device
		want Verdict
		why  string
	}{
		{find(ds, "sda"), Free, ""},
		{find(ds, "sdb"), Refused, "has partitions"},
		{find(ds, "sdb1"), Refused, "mounted at /"},
		{find(ds, "sdb5"), Refused, "in use as swap"},
		{find(ds, "sr0"), Refused, "not a disk or partition"},
		{find(ds, "sdb2"), Refused, "smaller than the 64 MiB"},
		{Device{Name: "sdc", Type: "disk", Size: 1 << 40, FSType: "zfs_member"}, Overwrite, "holds zfs_member"},
		{Device{Name: "sdc", Type: "disk", Size: 1 << 40, Held: true}, Refused, "device-mapper"},
		{Device{Name: "sdc", Type: "disk", Size: 1 << 40, InPool: "tank"}, Refused, "imported pool tank"},
		{Device{Name: "sdc", Type: "disk", Size: 1 << 40, ReadOnly: true}, Refused, "read-only"},
		{Device{Name: "sdc", Type: "disk", Size: 1 << 40, HoldsFS: true, FSType: "ext4"}, Refused, "root filesystem"},
	} {
		got, why := Judge(c.dev, ds)
		if got != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%s: %v %q, want %v %q", c.dev.Name, got, why, c.want, c.why)
		}
	}
}

func TestByIDPrefersStableNames(t *testing.T) {
	dir := t.TempDir()
	for name, target := range map[string]string{
		"scsi-0QEMU_disk": "../../sda", "wwn-0x5000": "../../sda", "ata-DISK_1-part1": "../../sda1",
		"usb-stick": "../../sdc", "nvme-eui.0025": "../../nvme0n1", "nvme-Samsung_X": "../../nvme0n1",
	} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ByID(dir)
	if err != nil {
		t.Fatal(err)
	}
	for kernel, want := range map[string]string{"sda": "wwn-0x5000", "sda1": "ata-DISK_1-part1", "sdc": "usb-stick", "nvme0n1": "nvme-eui.0025"} {
		if filepath.Base(got[kernel]) != want {
			t.Errorf("%s -> %s, want %s", kernel, got[kernel], want)
		}
	}
}

type fakeRun struct{ out []byte }

func (f fakeRun) Read(context.Context, execute.Argv) (execute.Result, error) {
	return execute.Result{Stdout: f.out}, nil
}

// The source marks pool members, partitions and their disks alike, and
// uses by-id names where there are any.
func TestSourceMarksPoolMembers(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "linux", "lsblk.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink("../../sda", filepath.Join(dir, "wwn-0x5000")); err != nil {
		t.Fatal(err)
	}
	src := Source{Run: fakeRun{b}, ByIDDir: dir, PoolDevices: func(context.Context) (map[string][]string, error) {
		return map[string][]string{"tank": {"/dev/sdb5"}}, nil
	}}
	ds, err := src.Devices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if sda := find(ds, "sda"); sda.ByID != filepath.Join(dir, "wwn-0x5000") || sda.InPool != "" {
		t.Errorf("sda = %+v", sda)
	}
	if find(ds, "sdb5").InPool != "tank" || find(ds, "sdb").InPool != "tank" || find(ds, "sdb1").ByID != "/dev/sdb1" {
		t.Errorf("pool membership or by-id fallback wrong: %+v", ds)
	}
}

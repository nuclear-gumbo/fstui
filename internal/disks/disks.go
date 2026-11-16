// SPDX-License-Identifier: GPL-3.0-or-later

// Package disks lists block devices read-only and decides which of them
// may be given to a new pool. It never opens a device: lsblk reads udev's
// database and sysfs, and the by-id names and holders come from
// directory listings.
package disks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Device is a disk or partition as lsblk reports it.
type Device struct {
	Name        string   `json:"name"` // kernel name, e.g. sda1
	Path        string   `json:"path"` // /dev/sda1
	Size        uint64   `json:"size"` // bytes
	Type        string   `json:"type"` // disk, part, rom, loop, ...
	FSType      string   `json:"fstype"`
	Mountpoints []string `json:"mountpoints"`
	Parent      string   `json:"pkname"`
	Model       string   `json:"model"`
	Serial      string   `json:"serial"`
	WWN         string   `json:"wwn"`
	Rotational  bool     `json:"rota"`
	ReadOnly    bool     `json:"ro"`
	Children    []Device `json:"children"`

	// Set by Inventory, not lsblk.
	ByID    string // the stable /dev/disk/by-id path, or Path when there is none
	Held    bool   // held by device-mapper, md or LVM
	InPool  string // the imported pool using it, if any
	HoldsFS bool   // the disk or a partition of it is mounted at /
}

// LsblkArgs are lsblk's options; the columns are what Device reads.
var LsblkArgs = []string{"-J", "-b", "-o", "NAME,PATH,SIZE,TYPE,FSTYPE,MOUNTPOINTS,PKNAME,MODEL,SERIAL,WWN,ROTA,RO"}

// ParseLsblk parses lsblk -J -b output into a flat list: every disk, then
// its partitions.
func ParseLsblk(data []byte) ([]Device, error) {
	var doc struct {
		Devices []Device `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("lsblk: %w", err)
	}
	var out []Device
	var walk func([]Device)
	walk = func(ds []Device) {
		for _, d := range ds {
			d.Mountpoints = slices.DeleteFunc(d.Mountpoints, func(m string) bool { return m == "" })
			kids := d.Children
			out = append(out, d)
			walk(kids)
		}
	}
	walk(doc.Devices)
	return out, nil
}

// byIDRank orders by-id names from most to least stable.
var byIDRank = []string{"wwn-", "nvme-eui.", "nvme-", "ata-", "scsi-"}

func rank(name string) int {
	for i, p := range byIDRank {
		if strings.HasPrefix(name, p) {
			return i
		}
	}
	return len(byIDRank)
}

// ByID maps kernel names (sda, sda1) to their most stable link in dir
// (/dev/disk/by-id).
func ByID(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		kernel := filepath.Base(target)
		if cur, ok := out[kernel]; !ok || rank(e.Name()) < rank(filepath.Base(cur)) ||
			(rank(e.Name()) == rank(filepath.Base(cur)) && e.Name() < filepath.Base(cur)) {
			out[kernel] = filepath.Join(dir, e.Name())
		}
	}
	return out, nil
}

// Held reports whether sysfs lists holders for the device: device-mapper,
// md or LVM using it.
func Held(sysBlock, name string) bool {
	entries, err := os.ReadDir(filepath.Join(sysBlock, name, "holders"))
	return err == nil && len(entries) > 0
}

// MinSize is the smallest device ZFS accepts in a pool (SPA_MINDEVSIZE).
const MinSize = 64 << 20

// Verdict is what a new pool may do with a device.
type Verdict int

const (
	// Free is a device with no signature, not in use.
	Free Verdict = iota
	// Overwrite is a device holding a signature; usable only if the user
	// chooses to overwrite it.
	Overwrite
	// Refused is a device in use, or not one a pool may use. Nothing
	// overrides it.
	Refused
)

// Judge decides what a pool may do with d, and says why when it is not
// Free. all is the whole inventory, for a disk's partitions.
func Judge(d Device, all []Device) (Verdict, string) {
	switch {
	case d.Type != "disk" && d.Type != "part":
		return Refused, "not a disk or partition (" + d.Type + ")"
	case d.ReadOnly:
		return Refused, "read-only"
	case d.Size < MinSize:
		return Refused, "smaller than the 64 MiB ZFS needs"
	case d.HoldsFS:
		return Refused, "holds the root filesystem"
	case slices.Contains(d.Mountpoints, "[SWAP]"): // lsblk's mark for active swap
		return Refused, "in use as swap"
	case len(d.Mountpoints) > 0:
		return Refused, "mounted at " + strings.Join(d.Mountpoints, ", ")
	case d.Held:
		return Refused, "in use by device-mapper, md or LVM"
	case d.InPool != "":
		return Refused, "in the imported pool " + d.InPool
	}
	if d.Type == "disk" {
		for _, p := range all {
			if p.Parent == d.Name {
				return Refused, "has partitions; choose an unused partition instead"
			}
		}
	}
	if d.FSType != "" {
		return Overwrite, "holds " + d.FSType
	}
	return Free, ""
}

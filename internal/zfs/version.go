// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"fmt"
	"strconv"
	"strings"
)

// Release is an OpenZFS release number.
type Release struct {
	Major, Minor, Patch int
}

func (r Release) String() string { return fmt.Sprintf("%d.%d.%d", r.Major, r.Minor, r.Patch) }

// AtLeast reports whether r is the same as or newer than o.
func (r Release) AtLeast(o Release) bool {
	if r.Major != o.Major {
		return r.Major > o.Major
	}
	if r.Minor != o.Minor {
		return r.Minor > o.Minor
	}
	return r.Patch >= o.Patch
}

// Version is the output of "zfs version -j": the userland tools and the
// loaded kernel module, which can differ after an upgrade until a reboot.
type Version struct {
	Userland        string // e.g. "zfs-2.3.9-0+deb13u1"
	Kernel          string // e.g. "zfs-kmod-2.3.9-0+deb13u1"
	UserlandRelease Release
	KernelRelease   Release
}

// Mismatched reports whether userland and kernel module releases differ.
func (v Version) Mismatched() bool { return v.UserlandRelease != v.KernelRelease }

// ParseVersion parses the output of "zfs version -j".
func ParseVersion(data []byte) (Version, error) {
	var doc struct {
		ZFSVersion struct {
			Userland string `json:"userland"`
			Kernel   string `json:"kernel"`
		} `json:"zfs_version"`
	}
	if err := decode(data, "zfs version", &doc); err != nil {
		return Version{}, err
	}
	return newVersion(doc.ZFSVersion.Userland, doc.ZFSVersion.Kernel)
}

func newVersion(userland, kernel string) (Version, error) {
	v := Version{Userland: userland, Kernel: kernel}
	var err error
	if v.UserlandRelease, err = parseRelease(userland, "zfs-"); err != nil {
		return Version{}, err
	}
	if v.KernelRelease, err = parseRelease(kernel, "zfs-kmod-"); err != nil {
		return Version{}, err
	}
	return v, nil
}

// parseRelease extracts 2.3.9 from "zfs-2.3.9-0+deb13u1" or
// "zfs-kmod-2.3.9-1". Distribution suffixes after the release are ignored.
func parseRelease(s, prefix string) (Release, error) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return Release{}, fmt.Errorf("version %q does not start with %q", s, prefix)
	}
	rel, _, _ := strings.Cut(rest, "-")
	parts := strings.Split(rel, ".")
	if len(parts) != 3 {
		return Release{}, fmt.Errorf("version %q: release %q is not major.minor.patch", s, rel)
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Release{}, fmt.Errorf("version %q: %w", s, err)
		}
		nums[i] = n
	}
	return Release{nums[0], nums[1], nums[2]}, nil
}

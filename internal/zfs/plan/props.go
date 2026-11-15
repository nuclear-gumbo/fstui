// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"fmt"
	"math/bits"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Property values are checked against the property's type before they reach
// argv. Only the properties below can be set from fstui; others are refused
// rather than passed through unchecked.

type kind int

const (
	kEnum  kind = iota
	kSize       // bytes with an optional K/M/G/T/P suffix, or "none" where allowed
	kBlock      // power of two
	kMount      // absolute path, none or legacy
)

type propType struct {
	kind   kind
	values []string // kEnum
	none   bool     // kSize: "none" allowed
	min    uint64   // kBlock
	max    uint64
}

var (
	onOff        = []string{"on", "off"}
	compressions = regexp.MustCompile(`^(on|off|lz4|lzjb|zle|gzip|gzip-[1-9]|zstd|zstd-([1-9]|1[0-9])|zstd-fast|zstd-fast-([1-9]|[1-9][0-9]|[1-9]00|1000))$`)
)

// settable lists the properties fstui sets and their types (zfsprops(7),
// OpenZFS 2.3.9).
var settable = map[string]propType{
	"compression":          {kind: kEnum}, // checked by compressions
	"atime":                {kind: kEnum, values: onOff},
	"relatime":             {kind: kEnum, values: onOff},
	"readonly":             {kind: kEnum, values: onOff},
	"exec":                 {kind: kEnum, values: onOff},
	"setuid":               {kind: kEnum, values: onOff},
	"devices":              {kind: kEnum, values: onOff},
	"canmount":             {kind: kEnum, values: []string{"on", "off", "noauto"}},
	"checksum":             {kind: kEnum, values: []string{"on", "off", "fletcher2", "fletcher4", "sha256", "noparity", "sha512", "skein", "edonr", "blake3"}},
	"copies":               {kind: kEnum, values: []string{"1", "2", "3"}},
	"dedup":                {kind: kEnum, values: []string{"on", "off", "verify", "sha256", "sha256,verify", "sha512", "sha512,verify", "skein", "skein,verify", "edonr,verify", "blake3", "blake3,verify"}},
	"sync":                 {kind: kEnum, values: []string{"standard", "always", "disabled"}},
	"xattr":                {kind: kEnum, values: []string{"on", "off", "sa", "dir"}},
	"logbias":              {kind: kEnum, values: []string{"latency", "throughput"}},
	"primarycache":         {kind: kEnum, values: []string{"all", "none", "metadata"}},
	"secondarycache":       {kind: kEnum, values: []string{"all", "none", "metadata"}},
	"snapdir":              {kind: kEnum, values: []string{"hidden", "visible", "disabled"}},
	"acltype":              {kind: kEnum, values: []string{"off", "nfsv4", "posix", "noacl", "posixacl"}},
	"recordsize":           {kind: kBlock, min: 512, max: 16 << 20},
	"special_small_blocks": {kind: kBlock, min: 0, max: 16 << 20},
	"quota":                {kind: kSize, none: true},
	"refquota":             {kind: kSize, none: true},
	"reservation":          {kind: kSize, none: true},
	"refreservation":       {kind: kSize, none: true},
	"mountpoint":           {kind: kMount},
}

var userProp = regexp.MustCompile(`^[a-z0-9_.+-]+:[a-z0-9_.:+-]+$`)

// checkProperty checks that prop can be set by fstui and value fits its
// type. It returns the value as it goes into argv.
func checkProperty(prop, value string) (string, error) {
	if userProp.MatchString(prop) {
		return value, freeForm("the value", value)
	}
	t, ok := settable[prop]
	if !ok {
		return "", fmt.Errorf("fstui does not set %s; properties it sets: %s, or a user property (module:name)", show(prop), strings.Join(settableNames(), ", "))
	}
	switch t.kind {
	case kEnum:
		return checkEnum(prop, value, t)
	case kSize:
		return checkSize(prop, value, t)
	case kBlock:
		return checkBlock(prop, value, t)
	case kMount:
		return checkMount(value)
	}
	return "", fmt.Errorf("%s: no check for this property", prop)
}

func checkEnum(prop, value string, t propType) (string, error) {
	if prop == "compression" {
		if compressions.MatchString(value) {
			return value, nil
		}
		return "", fmt.Errorf("compression: %s is not an algorithm OpenZFS 2.3 knows", show(value))
	}
	if slices.Contains(t.values, value) {
		return value, nil
	}
	return "", fmt.Errorf("%s: %s is not one of %s", prop, show(value), strings.Join(t.values, ", "))
}

func checkSize(prop, value string, t propType) (string, error) {
	if t.none && value == "none" {
		return value, nil
	}
	n, err := ParseSize(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", prop, err)
	}
	return strconv.FormatUint(n, 10), nil
}

func checkBlock(prop, value string, t propType) (string, error) {
	n, err := ParseSize(value)
	ok := err == nil && n <= t.max && (n == 0 && t.min == 0 || bits.OnesCount64(n) == 1 && n >= t.min)
	if !ok {
		return "", fmt.Errorf("%s: %s is not a power of two from %d to %d bytes", prop, show(value), max(t.min, 1), t.max)
	}
	return strconv.FormatUint(n, 10), nil
}

func checkMount(value string) (string, error) {
	if value == "none" || value == "legacy" || path.IsAbs(value) && path.Clean(value) == value && freeForm("the mountpoint", value) == nil {
		return value, nil
	}
	return "", fmt.Errorf("mountpoint: %s is not an absolute path (without . or ..), none or legacy", show(value))
}

func settableNames() []string {
	names := make([]string, 0, len(settable))
	for n := range settable {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

var sizeRe = regexp.MustCompile(`^([0-9]+)([KMGTP]?)$`)

// ParseSize reads a whole size such as 512, 10G, 4K or 4KiB; suffixes
// are powers of 1024, as in ZFS.
func ParseSize(s string) (uint64, error) {
	u := strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(s), "B"), "I")
	m := sizeRe.FindStringSubmatch(u)
	if m == nil {
		return 0, fmt.Errorf("%s is not a size (a whole number with an optional K, M, G, T or P)", show(s))
	}
	n, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is too large", show(s))
	}
	var shift uint
	if m[2] != "" {
		shift = uint(strings.Index("KMGTP", m[2])+1) * 10
	}
	if n > (^uint64(0))>>shift {
		return 0, fmt.Errorf("%s is too large", show(s))
	}
	return n << shift, nil
}

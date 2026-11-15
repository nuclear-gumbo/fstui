// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// sizeScale turns the recordings' 512 MiB sparse files into 4 TiB disks so the
// demo looks like a real system. Every byte quantity is multiplied by the same
// factor, so percentages, ratios and fragmentation stay as recorded and the
// figures agree with each other.
const sizeScale = 8192

// byteProperties are pool and dataset properties that are byte counts
// (zpoolprops(7), zfsprops(7)). Block sizes such as recordsize are not.
var byteProperties = map[string]bool{
	"size": true, "allocated": true, "free": true, "freeing": true, "leaked": true, "expandsize": true,
	"checkpoint": true, "bcloneused": true, "bclonesaved": true, "dedup_table_size": true,
	"used": true, "available": true, "referenced": true, "logicalused": true, "logicalreferenced": true,
	"quota": true, "refquota": true, "reservation": true, "refreservation": true, "volsize": true,
	"usedbydataset": true, "usedbysnapshots": true, "usedbychildren": true, "usedbyrefreservation": true,
	"written": true,
}

// statusBytes are the byte counts in zpool status -j: device space, scan
// progress and checkpoint space.
var statusBytes = map[string]bool{
	"alloc_space": true, "total_space": true, "def_space": true, "rep_dev_size": true, "phys_space": true,
	"to_examine": true, "examined": true, "issued": true, "processed": true, "skipped": true,
	"scan_processed": true, "bytes_per_scan": true, "issued_bytes_per_scan": true,
	"checkpoint_space": true, "space": true, "self_healed": true,
}

// scaleJSON multiplies the byte quantities of a recorded zpool or zfs
// JSON document by sizeScale.
func scaleJSON(b []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("demo recording: %w", err)
	}
	scaleNode(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("demo recording: %w", err)
	}
	return out, nil
}

func scaleNode(n any) {
	switch v := n.(type) {
	case map[string]any:
		for k, child := range v {
			if !scaleEntry(v, k, child) {
				scaleNode(child)
			}
		}
	case []any:
		for _, child := range v {
			scaleNode(child)
		}
	}
}

// scaleEntry scales m[k] if it is a byte quantity and reports whether it
// was one: a byte property ({"value": ..., "source": ...} under its
// name) or a byte count of zpool status.
func scaleEntry(m map[string]any, k string, child any) bool {
	if prop, ok := child.(map[string]any); ok && byteProperties[k] {
		if s, ok := scaled(prop["value"]); ok {
			prop["value"] = s
		}
		return true
	}
	if statusBytes[k] {
		if s, ok := scaled(child); ok {
			m[k] = s
			return true
		}
	}
	return false
}

// scaled multiplies a JSON number or a digit string (-p output), keeping
// its form; anything else ("-", "none", "auto") is left alone.
func scaled(v any) (any, bool) {
	var digits string
	switch x := v.(type) {
	case json.Number:
		digits = x.String()
	case string:
		digits = x
	default:
		return nil, false
	}
	n, ok := new(big.Int).SetString(strings.TrimSpace(digits), 10)
	if !ok || n.Sign() <= 0 {
		return nil, false
	}
	s := n.Mul(n, big.NewInt(sizeScale)).String()
	if _, isNum := v.(json.Number); isNum {
		return json.Number(s), true
	}
	return s, true
}

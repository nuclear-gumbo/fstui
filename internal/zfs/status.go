// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// Vdev is one node of a pool's device tree.
type Vdev struct {
	Name           termtext.Text // as zpool prints it: "mirror-0", or a device path
	Type           string        // root, mirror, raidz, draid, replacing, spare, disk, file, ...
	Class          string        // normal, special, log, l2cache, spare, dedup
	State          string        // ONLINE, DEGRADED, FAULTED, OFFLINE, UNAVAIL, REMOVED, AVAIL, INUSE
	GUID           uint64
	ReadErrors     uint64
	WriteErrors    uint64
	ChecksumErrors uint64
	SlowIOs        uint64
	SelfHealed     uint64 // bytes repaired on this device; 0 when none
	Children       []Vdev
}

// Scan is the state of the pool's current or last scrub or resilver.
type Scan struct {
	Function  string // SCRUB or RESILVER
	State     string // SCANNING, FINISHED, CANCELED
	Paused    bool   // a paused scrub still reports State SCANNING
	PausedAt  time.Time
	Start     time.Time
	End       time.Time // zero while scanning
	ToExamine uint64
	Examined  uint64
	Issued    uint64
	Processed uint64 // bytes repaired
	Errors    uint64
}

// Checkpoint is a pool checkpoint (zpool-checkpoint(8)). While one exists, ZFS keeps every block
// that was in use when it was taken, and refuses remove, attach, detach,
// split and reguid.
type Checkpoint struct {
	State string    // EXISTS, or DISCARDING while zpool checkpoint -d runs
	Taken time.Time // when the checkpoint was taken
	Space uint64    // bytes held only because of the checkpoint
}

// PoolStatus is one pool from "zpool status -j --json-int".
type PoolStatus struct {
	Name       name.Pool
	State      string
	GUID       uint64
	ErrorCount uint64
	Status     []termtext.Text // explanation of a problem, one line per element
	Action     []termtext.Text // what to do about it
	Scan       *Scan           // nil if the pool was never scanned
	Checkpoint *Checkpoint     // nil if the pool has none
	Data       Vdev            // the root of the data vdevs
	Special    []Vdev
	Dedup      []Vdev
	Logs       []Vdev
	Cache      []Vdev
	Spares     []Vdev
}

type jsonVdev struct {
	Name           string          `json:"name"`
	VdevType       string          `json:"vdev_type"`
	Class          string          `json:"class"`
	State          string          `json:"state"`
	GUID           uint64          `json:"guid"`
	ReadErrors     uint64          `json:"read_errors"`
	WriteErrors    uint64          `json:"write_errors"`
	ChecksumErrors uint64          `json:"checksum_errors"`
	SlowIOs        uint64          `json:"slow_ios"`
	SelfHealed     uint64          `json:"self_healed"`
	Vdevs          json.RawMessage `json:"vdevs"`
}

type jsonScan struct {
	Function   string `json:"function"`
	State      string `json:"state"`
	StartTime  int64  `json:"start_time"`
	EndTime    int64  `json:"end_time"`
	ToExamine  uint64 `json:"to_examine"`
	Examined   uint64 `json:"examined"`
	Issued     uint64 `json:"issued"`
	Processed  uint64 `json:"processed"`
	Errors     uint64 `json:"errors"`
	ScrubPause int64  `json:"scrub_pause"`
}

type jsonCheckpoint struct {
	State     string `json:"state"`
	StartTime int64  `json:"start_time"`
	Space     uint64 `json:"space"`
}

type jsonPoolStatus struct {
	Name       string          `json:"name"`
	State      string          `json:"state"`
	GUID       uint64          `json:"pool_guid"`
	ErrorCount uint64          `json:"error_count"`
	Status     string          `json:"status"`
	Action     string          `json:"action"`
	ScanStats  *jsonScan       `json:"scan_stats"`
	Checkpoint *jsonCheckpoint `json:"checkpoint_stats"`
	Vdevs      json.RawMessage `json:"vdevs"`
	Special    json.RawMessage `json:"special"`
	Dedup      json.RawMessage `json:"dedup"`
	Logs       json.RawMessage `json:"logs"`
	Cache      json.RawMessage `json:"l2cache"`
	Spares     json.RawMessage `json:"spares"`
}

// ParsePoolStatus parses "zpool status -j --json-int" for one or more
// pools, sorted by name.
func ParsePoolStatus(data []byte) ([]PoolStatus, error) {
	var doc struct {
		Pools map[string]jsonPoolStatus `json:"pools"`
	}
	if err := decode(data, "zpool status", &doc); err != nil {
		return nil, err
	}
	out := make([]PoolStatus, 0, len(doc.Pools))
	for _, jp := range doc.Pools {
		p, err := jp.parse()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name.String() < out[j].Name.String() })
	return out, nil
}

func (jp jsonPoolStatus) parse() (PoolStatus, error) {
	pool, err := name.ParsePool(jp.Name)
	if err != nil {
		return PoolStatus{}, fmt.Errorf("zpool status: pool %s: %w", termtext.New(jp.Name), err)
	}
	p := PoolStatus{
		Name: pool, State: jp.State, GUID: jp.GUID, ErrorCount: jp.ErrorCount,
		Status: lines(jp.Status), Action: lines(jp.Action),
	}
	if jp.ScanStats != nil {
		p.Scan = jp.ScanStats.parse()
	}
	if c := jp.Checkpoint; c != nil {
		p.Checkpoint = &Checkpoint{State: c.State, Taken: unixTime(c.StartTime), Space: c.Space}
	}
	data, err := parseVdevs(jp.Vdevs)
	if err != nil {
		return PoolStatus{}, fmt.Errorf("zpool status: pool %s: %w", pool, err)
	}
	if len(data) != 1 {
		return PoolStatus{}, fmt.Errorf("zpool status: pool %s: %d root vdevs, want 1", pool, len(data))
	}
	p.Data = data[0]
	for _, g := range []struct {
		dst *[]Vdev
		raw json.RawMessage
	}{{&p.Special, jp.Special}, {&p.Dedup, jp.Dedup}, {&p.Logs, jp.Logs}, {&p.Cache, jp.Cache}, {&p.Spares, jp.Spares}} {
		if *g.dst, err = parseVdevs(g.raw); err != nil {
			return PoolStatus{}, fmt.Errorf("zpool status: pool %s: %w", pool, err)
		}
	}
	return p, nil
}

func (s jsonScan) parse() *Scan {
	sc := &Scan{
		Function: s.Function, State: s.State,
		ToExamine: s.ToExamine, Examined: s.Examined, Issued: s.Issued,
		Processed: s.Processed, Errors: s.Errors,
		Start: unixTime(s.StartTime), End: unixTime(s.EndTime),
	}
	if s.ScrubPause != 0 {
		sc.Paused, sc.PausedAt = true, unixTime(s.ScrubPause)
	}
	return sc
}

// parseVdevs parses a vdev object, keeping document order.
func parseVdevs(raw json.RawMessage) ([]Vdev, error) {
	members, err := orderedObject(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Vdev, 0, len(members))
	for _, m := range members {
		var jv jsonVdev
		if err := json.Unmarshal(m.Value, &jv); err != nil {
			return nil, fmt.Errorf("vdev %s: %w", termtext.New(m.Key), err)
		}
		children, err := parseVdevs(jv.Vdevs)
		if err != nil {
			return nil, err
		}
		out = append(out, Vdev{
			Name: termtext.New(jv.Name), Type: jv.VdevType, Class: jv.Class, State: jv.State,
			GUID: jv.GUID, ReadErrors: jv.ReadErrors, WriteErrors: jv.WriteErrors,
			ChecksumErrors: jv.ChecksumErrors, SlowIOs: jv.SlowIOs, SelfHealed: jv.SelfHealed,
			Children: children,
		})
	}
	return out, nil
}

// lines splits OpenZFS's multi-line explanations, which use newlines and
// tab indentation for layout, into trimmed lines, so that only
// unexpected control characters end up as visible escapes.
func lines(s string) []termtext.Text {
	var out []termtext.Text
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, termtext.New(l))
		}
	}
	return out
}

func unixTime(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Activity is what a pool is doing, for planning changes to it: the
// scan, and each device's initialize and trim state.
type Activity struct {
	GUID      string
	ScanKind  string // SCRUB, RESILVER, ERRORSCRUB; empty if never scanned
	ScanState string // running, paused, finished, canceled; empty if never scanned
	Devices   []DeviceActivity
}

// DeviceActivity is one leaf device's initialize and trim state, as
// zpool status -i -t reports them (e.g. ACTIVE, SUSPENDED, COMPLETE).
type DeviceActivity struct {
	Name        string
	Init        string
	Trim        string
	TrimNotSupp bool
}

// Scanning reports whether a scan is running or paused.
func (a Activity) Scanning() bool { return a.ScanState == "running" || a.ScanState == "paused" }

// AnyDevice reports whether a device matches f.
func (a Activity) AnyDevice(f func(DeviceActivity) bool) bool {
	return slices.ContainsFunc(a.Devices, f)
}

type jsonActivityVdev struct {
	Name     string                      `json:"name"`
	VdevType string                      `json:"vdev_type"`
	Init     string                      `json:"init_state"`
	Trim     string                      `json:"trim_state"`
	NotSupp  uint64                      `json:"trim_notsup"`
	Vdevs    map[string]jsonActivityVdev `json:"vdevs"`
}

type jsonActivity struct {
	GUID  uint64                      `json:"pool_guid"`
	Scan  *jsonScan                   `json:"scan_stats"`
	Vdevs map[string]jsonActivityVdev `json:"vdevs"`
	// Device classes other than data are listed apart.
	Special map[string]jsonActivityVdev `json:"special"`
	Dedup   map[string]jsonActivityVdev `json:"dedup"`
	Logs    map[string]jsonActivityVdev `json:"logs"`
}

// PoolActivity reads zpool status -i -t for one pool (validated by the
// caller).
func (r *Reader) PoolActivity(ctx context.Context, pool string) (Activity, error) {
	out, err := r.read(ctx, execute.Argv{
		Tool: execute.Zpool, Options: []string{"status", "-j", "--json-int", "-i", "-t"}, Operands: []string{pool},
	})
	if err != nil {
		return Activity{}, err
	}
	return ParseActivity(out, pool)
}

// ParseActivity parses zpool status -j --json-int -i -t for pool.
func ParseActivity(data []byte, pool string) (Activity, error) {
	var doc struct {
		Pools map[string]jsonActivity `json:"pools"`
	}
	if err := decode(data, "zpool status", &doc); err != nil {
		return Activity{}, err
	}
	jp, ok := doc.Pools[pool]
	if !ok {
		return Activity{}, fmt.Errorf("zpool status: no pool %q in the answer", pool)
	}
	a := Activity{GUID: strconv.FormatUint(jp.GUID, 10)}
	if s := jp.Scan; s != nil {
		a.ScanKind = s.Function
		switch {
		case s.State == "SCANNING" && s.ScrubPause != 0:
			a.ScanState = "paused"
		case s.State == "SCANNING":
			a.ScanState = "running"
		case s.State == "FINISHED":
			a.ScanState = "finished"
		case s.State == "CANCELED":
			a.ScanState = "canceled"
		default:
			return Activity{}, fmt.Errorf("zpool status: scan state %q is not one fstui knows", s.State)
		}
	}
	for _, group := range []map[string]jsonActivityVdev{jp.Vdevs, jp.Special, jp.Dedup, jp.Logs} {
		for _, v := range group {
			a.Devices = append(a.Devices, leaves(v)...)
		}
	}
	slices.SortFunc(a.Devices, func(x, y DeviceActivity) int { return strings.Compare(x.Name, y.Name) })
	return a, nil
}

// leaves returns the devices without children below v.
func leaves(v jsonActivityVdev) []DeviceActivity {
	if len(v.Vdevs) == 0 {
		if v.VdevType == "root" {
			return nil
		}
		return []DeviceActivity{{Name: v.Name, Init: v.Init, Trim: v.Trim, TrimNotSupp: v.NotSupp > 0}}
	}
	var out []DeviceActivity
	for _, c := range v.Vdevs {
		out = append(out, leaves(c)...)
	}
	return out
}

// PoolTopology reads one pool's device tree (zpool status -j), for
// planning device changes.
func (r *Reader) PoolTopology(ctx context.Context, pool string) (PoolStatus, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"status", "-j", "--json-int"}, Operands: []string{pool}})
	if err != nil {
		return PoolStatus{}, err
	}
	st, err := ParsePoolStatus(out)
	if err != nil {
		return PoolStatus{}, err
	}
	for _, s := range st {
		if s.Name.String() == pool {
			return s, nil
		}
	}
	return PoolStatus{}, fmt.Errorf("zpool status: no pool %q in the answer", pool)
}

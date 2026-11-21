// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// satWindow is two minutes in which the devices did ops operations each
// with the given waits (ns), and the dmu_tx counters grew by throttle.
func satWindow(t *testing.T, ops uint64, waits map[string][2]uint64, throttle uint64, txgs []zfs.Txg) *Window {
	t.Helper()
	dmu, err := zfs.ParseKstat([]byte(fmt.Sprintf("1 1 0x01 1 1 1 1\nname type data\ndmu_tx_dirty_throttle 4 %d\n", throttle)))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_800_000_000, 0)
	var w Window
	w.Sample(zfs.TuneSample{At: t0, Txgs: []zfs.Txg{{TXG: 10, State: "C"}}})
	w.Sample(zfs.TuneSample{At: t0.Add(2 * time.Minute), DMUTx: dmu, Txgs: txgs})
	var rep []zfs.IOStat
	for name, wt := range waits {
		rep = append(rep, zfs.IOStat{Name: termtext.New(name), WriteOps: ops / 120, WriteWait: time.Duration(wt[0]), WriteDiskWait: time.Duration(wt[1])}) //nolint:gosec // test waits
	}
	w.AddIO(ioPool(rep...), 120)
	return &w
}

func TestSaturation(t *testing.T) {
	l := Layout{Leaves: []string{"a", "b", "c"}}
	ms := uint64(time.Millisecond)
	even := map[string][2]uint64{"a": {2 * ms, 2 * ms}, "b": {2 * ms, 2 * ms}, "c": {2 * ms, 2 * ms}}
	cases := []struct {
		name     string
		waits    map[string][2]uint64
		throttle uint64
		txgs     []zfs.Txg
		verdict  Verdict
		want     string
	}{
		{"fits", even, 0, nil, Fits, "nothing measured"},
		{"throttle", even, 12, nil, Mismatch, "write throttle delayed writers 12 times"},
		{"slow txgs", even, 0, []zfs.Txg{{TXG: 11, State: "C", SyncTime: uint64(8 * time.Second)}}, Mismatch, "take 8s to sync"},
		{"slow device", map[string][2]uint64{"a": {2 * ms, 2 * ms}, "b": {2 * ms, 2 * ms}, "c": {9 * ms, 9 * ms}}, 0, nil, WorthALook, "c waits 4.5 times"},
		{"queues", map[string][2]uint64{"a": {20 * ms, 2 * ms}, "b": {20 * ms, 2 * ms}, "c": {20 * ms, 2 * ms}}, 0, nil, WorthALook, "queue in ZFS"},
	}
	for _, c := range cases {
		f := Saturation(satWindow(t, 120000, c.waits, c.throttle, c.txgs), l, dt)
		if f.Verdict != c.verdict || !strings.Contains(f.Figure+" "+f.Points, c.want) {
			t.Errorf("%s: %s, %q, %q", c.name, f.Verdict.Word(), f.Figure, f.Points)
		}
	}
	if f := Saturation(satWindow(t, 100, even, 0, nil), l, dt); f.Verdict != NotEnough {
		t.Errorf("idle pool: %s", f.Verdict.Word())
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// The roles recording: a 3-wide raidz1 with a special mirror, a log, a
// cache and a spare. Cache and spare devices do not count.
func TestLayoutOf(t *testing.T) {
	b, err := os.ReadFile("../../testdata/zfs/2.3.9/roles/zpool-status.json")
	if err != nil {
		t.Fatal(err)
	}
	sts, err := zfs.ParsePoolStatus(b)
	if err != nil {
		t.Fatal(err)
	}
	l := LayoutOf(sts[0])
	if l.Factor != 1.5 || l.Desc != "a 3-wide raidz1" || !l.Raidz {
		t.Errorf("layout %+v", l)
	}
	for _, c := range sts[0].Cache {
		if slices.Contains(l.Leaves, c.Name.Raw()) {
			t.Errorf("cache device %s counted", c.Name.Raw())
		}
	}
	if len(l.Leaves) != 3+2+1 { // raidz members, special mirror, log
		t.Errorf("leaves %v", l.Leaves)
	}
}

// sample makes a reading of one dataset's counters.
func sample(t *testing.T, at time.Time, written, read, zilNormal uint64) zfs.TuneSample {
	t.Helper()
	k, err := zfs.ParseKstat([]byte(fmt.Sprintf("1 1 0x01 1 1 1 1\nname type data\ndataset_name 7 tank/db\nnwritten 4 %d\nnread 4 %d\nzil_itx_metaslab_normal_bytes 4 %d\n",
		written, read, zilNormal)))
	if err != nil {
		t.Fatal(err)
	}
	return zfs.TuneSample{At: at, Objsets: map[string]zfs.Kstat{"tank/db": k}}
}

func amplified(t *testing.T, written, zilNormal, diskWrite, diskRead float64) Finding {
	t.Helper()
	t0 := time.Unix(1_800_000_000, 0)
	var w Window
	w.Sample(sample(t, t0, 0, 0, 0))
	w.Sample(sample(t, t0.Add(2*time.Minute), uint64(written), 0, uint64(zilNormal)))
	w.AddIO(ioPool(zfs.IOStat{Name: termtext.New("sda"), WriteBytes: uint64(diskWrite / 120), ReadBytes: uint64(diskRead / 120)}), 120)
	return WriteAmplification(&w, Layout{Factor: 2, Desc: "a 2-way mirror", Leaves: []string{"sda"}}, 1, bytesOnly)
}

func TestWriteAmplification(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name                              string
		written, zil, diskWrite, diskRead float64
		verdict                           Verdict
		want                              string
	}{
		{"mirror", 100 * mib, 0, 210 * mib, 0, Fits, "2.1 times"},
		{"sync twice", 100 * mib, 60 * mib, 500 * mib, 0, WorthALook, "written twice"},
		{"read-modify-write", 100 * mib, 0, 900 * mib, 400 * mib, Mismatch, "read, changed and rewritten"},
		{"too little", 4 * mib, 0, 8 * mib, 0, NotEnough, "applications wrote"},
	}
	for _, c := range cases {
		f := amplified(t, c.written, c.zil, c.diskWrite, c.diskRead)
		if f.Verdict != c.verdict || !strings.Contains(f.Figure+" "+f.Points, c.want) {
			t.Errorf("%s: %s, %q, %q", c.name, f.Verdict.Word(), f.Figure, f.Points)
		}
	}
}

// Sync writes doubled on log devices are named as such, not blamed on
// the layout.
func TestAmplificationCauseLogDevice(t *testing.T) {
	got := amplificationCause(Layout{Factor: 2}, 100<<20, 0, 0, 60<<20, 0)
	if !strings.Contains(got, "also go to the log devices") {
		t.Errorf("cause %q", got)
	}
}

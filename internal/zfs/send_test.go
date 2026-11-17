// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sendRecording(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "zfs", "2.3.9", "send", name)) //nolint:gosec // test data
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Figures from testdata/zfs/2.3.9/send (scripts/capture-send.sh).
func TestParseSendEstimate(t *testing.T) {
	for _, c := range []struct {
		file    string
		streams int
		total   uint64
		from    string
	}{
		{"full.txt", 1, 100698992, ""},
		{"full-c.txt", 1, 33885040, ""},
		{"incr-i.txt", 1, 67131504, "fstuitest_send/data@s1"},
		{"incr-between.txt", 2, 67139808, "s1"},
		{"incr-bookmark.txt", 1, 67131504, "fstuitest_send/data#b1"},
		{"replicate-raw.txt", 7, 59384848, ""},
	} {
		e, err := ParseSendEstimate(sendRecording(t, c.file))
		if err != nil || len(e.Streams) != c.streams || e.Total != c.total || e.Streams[0].From != c.from {
			t.Errorf("%s: %+v, %v", c.file, e, err)
		}
	}
	// zfs prints only the size line of a stream it would refuse.
	if _, err := ParseSendEstimate(sendRecording(t, "missing.txt")); err == nil {
		t.Error("empty output parsed")
	}
	for _, bad := range []string{"full\tx\tnot-a-number\nsize\t1\n", "garbage\n", "full\tt@s\t1\n"} {
		if _, err := ParseSendEstimate([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestSendStepArgv(t *testing.T) {
	full := SendStep{To: "tank/a@1"}.Argv(SendOptions{Compressed: true}, true)
	if got := full.String(); got != "zfs send -n -P -c -- tank/a@1" {
		t.Errorf("full: %s", got)
	}
	inc := SendStep{From: "tank/a@1", To: "tank/a@3"}.Argv(SendOptions{Raw: true, Intermediates: true}, false)
	if got := inc.String(); got != "zfs send -w -I tank/a@1 -- tank/a@3" {
		t.Errorf("incremental: %s", got)
	}
}

func TestPlanSends(t *testing.T) {
	snap := func(n string, txg uint64) SnapshotRow { return SnapshotRow{Name: n, CreateTXG: txg} }
	marked := []SnapshotRow{snap("tank/b@x", 5), snap("tank/a@3", 30), snap("tank/a@1", 10), snap("tank/a@2", 20)}
	str := func(steps []SendStep) string {
		var s []string
		for _, st := range steps {
			s = append(s, st.From+">"+st.To)
		}
		return strings.Join(s, " ")
	}
	if got := str(PlanSends(marked, SendOptions{})); got != ">tank/a@1 tank/a@1>tank/a@2 tank/a@2>tank/a@3 >tank/b@x" {
		t.Errorf("chains: %s", got)
	}
	if got := str(PlanSends(marked, SendOptions{Intermediates: true})); got != ">tank/a@1 tank/a@1>tank/a@3 >tank/b@x" {
		t.Errorf("with -I: %s", got)
	}
}

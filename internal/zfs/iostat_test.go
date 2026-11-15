// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"strings"
	"testing"
	"time"
)

func parseIOStat(t *testing.T, text string) []IOSample {
	t.Helper()
	var got []IOSample
	p := IOStatParser{Sample: func(s IOSample) { got = append(got, s) }}
	for _, l := range strings.Split(text, "\n") {
		if err := p.Line(l); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

func TestIOStatParser(t *testing.T) {
	got := parseIOStat(t, string(golden(t, "iostat", "four-pools.txt")))
	// Three intervals were captured; the first is handed over when the
	// second header arrives, the others as soon as their last pool closes.
	if len(got) != 3 {
		t.Fatalf("%d samples, want 3", len(got))
	}
	s := got[1]
	names := make([]string, len(s))
	for i, p := range s {
		names[i] = p.Name.Display()
	}
	if strings.Join(names, ",") != "fstuitest_demo_backup,fstuitest_demo_roles,fstuitest_demo_scratch,fstuitest_demo_tank" {
		t.Fatalf("pools %v", names)
	}
	scratch := s[2]
	// Second interval of the capture: 377 write ops, 33581439 bytes
	// written, total_wait 196608 ns read, 6042696 ns write.
	if scratch.WriteOps != 377 || scratch.WriteBytes != 33581439 || scratch.ReadWait != 196608*time.Nanosecond ||
		!scratch.HasReadWait || scratch.WriteWait != 6042696*time.Nanosecond {
		t.Errorf("scratch = %+v", scratch.IOStat)
	}
	roles := s[1]
	var sections []string
	for _, d := range roles.Devices {
		sections = append(sections, d.Section+":"+d.Name.Display()+":"+string(rune('0'+d.Depth)))
	}
	want := []string{
		":raidz1-0:1",
		":/var/tmp/fstui-test/fstuitest_demo_xr1.img:2", ":/var/tmp/fstui-test/fstuitest_demo_xr2.img:2", ":/var/tmp/fstui-test/fstuitest_demo_xr3.img:2",
		"special:mirror-1:1",
		"special:/var/tmp/fstui-test/fstuitest_demo_xs1.img:2", "special:/var/tmp/fstui-test/fstuitest_demo_xs2.img:2",
		"logs:/var/tmp/fstui-test/fstuitest_demo_xl.img:1",
		"cache:/var/tmp/fstui-test/fstuitest_demo_xc.img:1",
	}
	if strings.Join(sections, " ") != strings.Join(want, " ") {
		t.Errorf("roles devices:\n%s\nwant\n%s", strings.Join(sections, "\n"), strings.Join(want, "\n"))
	}
	// An idle pool has no latency to report.
	if b := s[0]; b.HasReadWait || b.HasWriteWait || b.ReadOps != 0 {
		t.Errorf("idle pool: %+v", b.IOStat)
	}
}

func TestIOStatParserErrors(t *testing.T) {
	p := IOStatParser{}
	if err := p.Line("tank 1 2 3"); err == nil {
		t.Error("a short row was accepted")
	}
	vals := strings.Fields(strings.Repeat("0 ", iostatValues))
	vals[colReadWait] = "x"
	row := "tank " + strings.Join(vals, " ")
	if err := p.Line(row); err == nil || !strings.Contains(err.Error(), "latency") {
		t.Errorf("bad latency: %v", err)
	}
}

// A device path with spaces keeps them: only the trailing value columns
// are split off.
func TestSplitValuesKeepsNameSpaces(t *testing.T) {
	head, vals, ok := splitValues("    /dev/disk/by-id/my disk  1 2 3", 3)
	if !ok || head != "    /dev/disk/by-id/my disk" || strings.Join(vals, ",") != "1,2,3" {
		t.Errorf("head %q vals %v ok %v", head, vals, ok)
	}
}

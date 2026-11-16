// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

const gib = 1 << 30

func arcWorld(setMax uint64, conf []byte) Builder {
	r := world()
	r.arc = zfs.ARCLimits{SetMax: setMax, Stats: zfs.ARCStats{Size: 3 * gib, Min: 256 << 20, Max: 6 * gib, Memory: 8 * gib}}
	r.conf = conf
	return Builder{R: r}
}

func TestModprobeEdit(t *testing.T) {
	two, zero := uint64(2*gib), uint64(0)
	tests := []struct {
		old  string
		set  map[string]*uint64
		want string
	}{
		{"", map[string]*uint64{"zfs_arc_max": &two}, "options zfs zfs_arc_max=2147483648\n"},
		{
			"# mine\noptions zfs zfs_arc_max=1 zfs_txg_timeout=10\n",
			map[string]*uint64{"zfs_arc_max": &two},
			"# mine\noptions zfs zfs_arc_max=2147483648 zfs_txg_timeout=10\n",
		},
		{"options zfs zfs_arc_max=1\n", map[string]*uint64{"zfs_arc_max": &zero}, ""},
		{"# keep\noptions zfs zfs_arc_max=1\n", map[string]*uint64{"zfs_arc_max": &zero, "zfs_arc_min": nil}, "# keep\n"},
	}
	for _, tt := range tests {
		if got := modprobeEdit(tt.old, tt.set); got != tt.want {
			t.Errorf("modprobeEdit(%q) = %q, want %q", tt.old, got, tt.want)
		}
	}
}

func TestARCSet(t *testing.T) {
	p, err := arcWorld(0, nil).ARCSet(t.Context(), "2G", "")
	if err != nil || len(p.Blocked) != 0 || mutate.TierOf(p) != mutate.Medium {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if p.Steps[0].String() != "tee -- /sys/module/zfs/parameters/zfs_arc_max" || string(p.Steps[0].Input) != "2147483648\n" {
		t.Errorf("step %q input %q", p.Steps[0].String(), p.Steps[0].Input)
	}
	if !strings.Contains(p.Preview.Affects[1].Display(), "shrinks to the new maximum only when memory is needed") {
		t.Errorf("affects %v", p.Preview.Affects)
	}
	for _, bad := range [][2]string{{"32M", ""}, {"16G", ""}, {"", "16M"}, {"1G", "2G"}} {
		if p, _ := arcWorld(0, nil).ARCSet(t.Context(), bad[0], bad[1]); len(p.Blocked) == 0 {
			t.Errorf("max %q min %q not blocked", bad[0], bad[1])
		}
	}
	if p, _ := arcWorld(2*gib, nil).ARCSet(t.Context(), "0", ""); len(p.Blocked) == 0 {
		t.Error("back to automatic while loaded was not blocked")
	}
	both, _ := arcWorld(0, nil).ARCSet(t.Context(), "128M", "64M")
	if !strings.Contains(both.Steps[0].String(), "zfs_arc_min") {
		t.Errorf("lowering the maximum below the current minimum must set the minimum first: %v", both.Steps)
	}
}

func TestARCPersist(t *testing.T) {
	p, err := arcWorld(0, []byte("# mine\n")).ARCPersist(t.Context(), "2G", "")
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	var got []string
	for _, s := range p.Steps {
		got = append(got, s.String())
	}
	want := "cp -p -- /etc/modprobe.d/zfs.conf /etc/modprobe.d/zfs.conf.bak | tee -- /etc/modprobe.d/zfs.conf.fstui-new | " +
		"chmod 0644 -- /etc/modprobe.d/zfs.conf.fstui-new | mv -- /etc/modprobe.d/zfs.conf.fstui-new /etc/modprobe.d/zfs.conf"
	if strings.Join(got, " | ") != want || string(p.Steps[1].Input) != "# mine\noptions zfs zfs_arc_max=2147483648\n" {
		t.Errorf("steps %q, new file %q", got, p.Steps[1].Input)
	}
	if !strings.Contains(p.Preview.Affects[1].Display(), "+ options zfs zfs_arc_max=2147483648") {
		t.Errorf("affects %v", p.Preview.Affects)
	}
	none, _ := arcWorld(0, nil).ARCPersist(t.Context(), "2G", "")
	if strings.HasPrefix(none.Steps[0].String(), "cp") {
		t.Error("backed up a file that does not exist")
	}
	same, _ := arcWorld(0, []byte("options zfs zfs_arc_max=2147483648\n")).ARCPersist(t.Context(), "2G", "")
	if len(same.Blocked) == 0 {
		t.Error("writing what the file already says was not blocked")
	}
}

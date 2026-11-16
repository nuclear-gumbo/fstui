// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func TestPoolExport(t *testing.T) {
	f := checkpointed(false)
	p, err := Builder{R: f}.PoolExport(t.Context(), "tank")
	if err != nil || len(p.Blocked) != 0 || p.Steps[0].String() != "zpool export -- tank" || mutate.TierOf(p) != mutate.High {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if !strings.Contains(p.Preview.Affects[0].Raw(), "the 3 mounted filesystems of tank") {
		t.Errorf("affects %v", p.Preview.Affects)
	}
	f.objects["tank"][0].Properties["mountpoint"] = zfs.Property{Text: termtext.New("/")}
	p, _ = Builder{R: f}.PoolExport(t.Context(), "tank")
	if !strings.Contains(blocked(p), "running system's root") {
		t.Errorf("root pool: %q", blocked(p))
	}
}

func importable(state string, status ...string) *fakeReader {
	f := &fakeReader{names: []string{"tank"}}
	var st []termtext.Text
	for _, s := range status {
		st = append(st, termtext.New(s))
	}
	f.found = []zfs.Importable{{Name: termtext.New("backup"), ID: "4866379416960248110", State: state, Status: st}}
	return f
}

func TestPoolImport(t *testing.T) {
	s := ImportSpec{ID: "4866379416960248110", ReadOnly: true, NoMount: true, Dirs: []string{"/var/tmp/x"}}
	p, err := Builder{R: importable("ONLINE")}.PoolImport(t.Context(), s)
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("plan %+v, %v", p, err)
	}
	if got := p.Steps[0].String(); got != "zpool import -N -o readonly=on -d /var/tmp/x -- 4866379416960248110" {
		t.Errorf("command %s", got)
	}
	s.NewName = "old"
	p, _ = Builder{R: importable("ONLINE")}.PoolImport(t.Context(), s)
	if !strings.HasSuffix(p.Steps[0].String(), "-- 4866379416960248110 old") || p.Summary != "Import backup as old" {
		t.Errorf("renamed: %s, %q", p.Steps[0], p.Summary)
	}
}

func TestPoolImportRefusals(t *testing.T) {
	id := "4866379416960248110"
	cases := []struct {
		name string
		f    *fakeReader
		s    ImportSpec
		want string
	}{
		{"name taken", importable("ONLINE"), ImportSpec{ID: id, NewName: "tank"}, "named tank is imported"},
		{"other system", importable("ONLINE", "The pool was last accessed by another system."), ImportSpec{ID: id}, "another system"},
		{"unavailable", importable("UNAVAIL"), ImportSpec{ID: id}, "cannot be imported (UNAVAIL)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Builder{R: c.f}.PoolImport(t.Context(), c.s)
			if err != nil || !strings.Contains(blocked(p), c.want) {
				t.Errorf("blocked %q, %v; want %q", blocked(p), err, c.want)
			}
		})
	}
	for _, s := range []ImportSpec{{ID: "12; rm"}, {ID: id, Dirs: []string{"relative"}}, {ID: id, Dirs: []string{"/a\n/b"}}, {ID: id, NewName: "1bad"}, {ID: "1"}} {
		if _, err := (Builder{R: importable("ONLINE")}).PoolImport(t.Context(), s); err == nil {
			t.Errorf("%+v: no error", s)
		}
	}
	p, _ := Builder{R: importable("DEGRADED")}.PoolImport(t.Context(), ImportSpec{ID: id})
	if len(p.Blocked) != 0 || !strings.Contains(p.Preview.Affects[0].Raw(), "devices are missing") {
		t.Errorf("degraded: blocked %q, affects %v", blocked(p), p.Preview.Affects)
	}
}

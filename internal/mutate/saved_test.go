// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package mutate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

func store(t *testing.T) Store {
	t.Helper()
	return Store{
		Dir: filepath.Join(t.TempDir(), "plans"), Host: "dev", Version: "test",
		Now: func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) },
	}
}

func savedPlan() *Plan {
	return &Plan{
		Op: SnapshotCreate, Summary: "Create snapshot tank/a@x", Targets: []string{"tank/a"},
		Steps:    []execute.Argv{{Tool: execute.ZFS, Options: []string{"snapshot"}, Operands: []string{"tank/a@x"}}},
		Params:   map[string]string{"datasets": "tank/a", "snapshot": "x", "recursive": "false"},
		Baseline: Fingerprint{"tank/a": "guid 1", "tank/a@x": "absent"},
	}
}

func TestSaveLoadList(t *testing.T) {
	s := store(t)
	sha, err := s.Save(savedPlan())
	if err != nil {
		t.Fatal(err)
	}
	sv, err := s.Load(sha)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Op != "snapshot-create" || sv.Tier != "low" || sv.Steps[0][3] != "tank/a@x" || sv.Host != "dev" {
		t.Errorf("saved = %+v", sv)
	}
	fi, _ := os.Stat(s.Dir)
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode())
	}
	if err := s.Mark(sha, "applied", ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].State.State != "applied" || list[0].Err != nil {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if err := s.Delete(sha); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("after Delete: %+v", list)
	}
}

func TestLoadRefusesAChangedFile(t *testing.T) {
	s := store(t)
	sha, err := s.Save(savedPlan())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, sha+".json")
	data, _ := os.ReadFile(path)
	data[len(data)-2] = ' '
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(sha); err == nil {
		t.Fatal("a changed plan file loaded")
	}
	if _, err := s.Load("../../etc/passwd"); err == nil {
		t.Fatal("a path loaded as a plan")
	}
}

func TestEnableWriteIsNotSaved(t *testing.T) {
	if _, err := store(t).Save(&Plan{Op: EnableWrite}); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("Save = %v", err)
	}
}

func TestReapply(t *testing.T) {
	s := store(t)
	sha, _ := s.Save(savedPlan())
	sv, _ := s.Load(sha)
	same := func(context.Context, Op, map[string]string) (*Plan, error) { return savedPlan(), nil }
	p, err := Reapply(t.Context(), sha, sv, same)
	if err != nil || p.SavedSHA256 != sha {
		t.Fatalf("Reapply = %+v, %v", p, err)
	}
	changed := func(context.Context, Op, map[string]string) (*Plan, error) {
		q := savedPlan()
		q.Baseline["tank/a"] = "guid 2"
		return q, nil
	}
	var sc *StateChangedError
	if _, err := Reapply(t.Context(), sha, sv, changed); !errors.As(err, &sc) || len(sc.Changes) != 1 {
		t.Fatalf("Reapply on changed state = %v", err)
	}
}

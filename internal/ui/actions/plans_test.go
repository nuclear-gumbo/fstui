// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

func savedSession(t *testing.T, rebuild mutate.Rebuilder) *Session {
	t.Helper()
	dir := t.TempDir()
	tools, _ := execute.Resolve()
	return NewSession(Config{
		Tools: tools, Paths: Paths{Lock: filepath.Join(dir, "lock"), AuditDir: filepath.Join(dir, "audit"), Plans: filepath.Join(dir, "plans")},
		Offers:  func(ui.Target) (string, []Offer) { return "", nil },
		Rebuild: rebuild, Host: "dev", Version: "test", Now: time.Now,
	})
}

func snapPlan() *mutate.Plan {
	p := testPlan(mutate.SnapshotCreate, "tank/a")
	p.Params = map[string]string{"datasets": "tank/a", "snapshot": "x"}
	p.Baseline = mutate.Fingerprint{"tank/a": "guid 1", "tank/a@x": mutate.Absent}
	return p
}

// load runs the view's command and feeds its message back.
func load(t *testing.T, v *Plans, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command")
	}
	v.Update(cmd())
}

func TestPlansApplyOpensThePreview(t *testing.T) {
	s := savedSession(t, func(context.Context, mutate.Op, map[string]string) (*mutate.Plan, error) { return snapPlan(), nil })
	sha, err := s.store.Save(snapPlan())
	if err != nil {
		t.Fatal(err)
	}
	v := NewPlans(s)
	load(t, v, v.Init())
	if len(v.list) != 1 || v.list[0].SHA256 != sha {
		t.Fatalf("list = %+v", v.list)
	}
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd = v.Update(cmd())
	msg := cmd()
	om, ok := msg.(ui.OpenModalMsg)
	if !ok {
		t.Fatalf("apply gave %T, want a preview", msg)
	}
	c, ok := om.Modal.(*Confirm)
	if !ok || c.plan.SavedSHA256 != sha {
		t.Fatalf("modal = %+v", om.Modal)
	}
}

func TestPlansRefusesWhenStateChangedAndMarksIt(t *testing.T) {
	s := savedSession(t, func(context.Context, mutate.Op, map[string]string) (*mutate.Plan, error) {
		p := snapPlan()
		p.Baseline["tank/a@x"] = "guid 7"
		return p, nil
	})
	if _, err := s.store.Save(snapPlan()); err != nil {
		t.Fatal(err)
	}
	v := NewPlans(s)
	load(t, v, v.Init())
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd = v.Update(cmd())
	load(t, v, cmd)
	if !strings.Contains(v.message, "tank/a@x now exists") || v.list[0].State.State != "refused" {
		t.Fatalf("message %q, state %+v", v.message, v.list[0].State)
	}
}

func TestPlansDoesNotApplyTwice(t *testing.T) {
	s := savedSession(t, nil)
	sha, _ := s.store.Save(snapPlan())
	if err := s.store.Mark(sha, "applied", ""); err != nil {
		t.Fatal(err)
	}
	v := NewPlans(s)
	load(t, v, v.Init())
	if _, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || !strings.Contains(v.message, "can be used once") {
		t.Fatalf("message %q", v.message)
	}
}

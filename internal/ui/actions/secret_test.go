// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

func typeInto(f *Form, s string) {
	for _, r := range s {
		f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestSecretFieldsAreCheckedWipedAndNeverValues(t *testing.T) {
	var got []byte
	var gotValues map[string]string
	build := func(_ context.Context, v map[string]string, secret []byte) (*mutate.Plan, error) {
		got, gotValues = append([]byte(nil), secret...), v
		p := testPlan(mutate.KeyChange, "tank/sec")
		p.Secret = true
		return p, nil
	}
	f := NewForm("Change passphrase", []Field{
		{Name: "passphrase", Label: "Passphrase", Secret: true},
		{Name: "repeat", Label: "Again", Secret: true, Repeat: "passphrase"},
	}, nil, func(*mutate.Plan, Rebuild) ui.Modal { return nil }).withSecret(build)
	typeInto(f, "correct horse")
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	typeInto(f, "correct house")
	if out := render(f); strings.Contains(out, "horse") || !strings.Contains(out, "*************") {
		t.Fatalf("render shows the passphrase:\n%s", out)
	}
	_, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || !strings.Contains(f.err, "not the same") {
		t.Fatalf("different passphrases: err %q", f.err)
	}
	for _, s := range f.secrets {
		if len(s) != 0 || strings.ContainsRune(string(s[:cap(s)]), 'h') {
			t.Fatal("a typed passphrase was not wiped")
		}
	}
	f.focus = 0
	typeInto(f, "correct horse")
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	typeInto(f, "correct horse")
	_, cmd = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("no build: %q", f.err)
	}
	cmd()
	if string(got) != "correct horse" || gotValues["passphrase"] != "" || gotValues["repeat"] != "" {
		t.Fatalf("build got %q and values %v", got, gotValues)
	}
}

func TestPlansWithASecretAreNotSaved(t *testing.T) {
	s := savedSession(t, nil)
	p := snapPlan()
	p.Secret = true
	if _, err := s.store.Save(p); err == nil {
		t.Fatal("a plan with a passphrase was saved")
	}
}

// Name, enter, passphrase, enter, retype, enter.
func TestEnterFillsTheFormTopToBottom(t *testing.T) {
	built := false
	build := func(context.Context, map[string]string, []byte) (*mutate.Plan, error) {
		built = true
		return testPlan(mutate.DatasetCreate, "tank/x"), nil
	}
	f := NewForm("Create encrypted filesystem below", []Field{
		{Name: "dataset", Label: "Name", Default: "tank/"},
		{Name: "passphrase", Label: "Passphrase", Secret: true},
		{Name: "repeat", Label: "Retype passphrase", Secret: true, Repeat: "passphrase"},
	}, nil, func(*mutate.Plan, Rebuild) ui.Modal { return nil }).withSecret(build)
	typeInto(f, "x")
	f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if f.focus != 1 {
		t.Fatalf("after the name, focus %d", f.focus)
	}
	typeInto(f, "correct horse")
	f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if f.focus != 2 {
		t.Fatalf("after the passphrase, focus %d", f.focus)
	}
	typeInto(f, "correct horse")
	_, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("the last enter built nothing: %q", f.err)
	}
	cmd()
	if !built {
		t.Fatal("not built")
	}
}

// enter stops at every field, optional ones too, so every form moves
// the same way; ctrl+enter or alt+enter shows the change from any
// field, once the required ones (marked *) are filled.
func TestEnterStopsAtEveryFieldAndCtrlEnterSkips(t *testing.T) {
	built := 0
	build := func(context.Context, map[string]string) (*mutate.Plan, error) { //nolint:unparam // a Build
		built++
		return testPlan(mutate.DatasetCreate, "tank/x"), nil
	}
	newForm := func() *Form {
		return NewForm("Create filesystem below", []Field{
			{Name: "dataset", Label: "Name"},
			{Name: "mountpoint", Label: "Mountpoint (leave empty to inherit)", Optional: true},
			{Name: "quota", Label: "Quota (leave empty for none)", Optional: true},
		}, build, func(*mutate.Plan, Rebuild) ui.Modal { return nil })
	}
	f := newForm()
	out := render(f)
	if !strings.Contains(out, "Name*:") || strings.Contains(out, "Mountpoint (leave empty to inherit)*") || !strings.Contains(out, "* required") {
		t.Errorf("required fields are not marked:\n%s", out)
	}
	typeInto(f, "tank/x")
	for want := 1; want <= 2; want++ {
		if _, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || f.focus != want {
			t.Fatalf("enter: focus %d, want %d; command %v", f.focus, want, cmd != nil)
		}
	}
	if _, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("enter on the last field showed nothing")
	}

	for _, skip := range []tea.KeyPressMsg{{Code: tea.KeyEnter, Mod: tea.ModCtrl}, {Code: tea.KeyEnter, Mod: tea.ModAlt}} {
		f = newForm()
		if _, cmd := f.Update(skip); cmd != nil || f.err != "Name is required." {
			t.Fatalf("%s with no name: err %q", skip, f.err)
		}
		typeInto(f, "tank/x")
		if _, cmd := f.Update(skip); cmd == nil {
			t.Fatalf("%s did not skip to the change", skip)
		}
	}
}

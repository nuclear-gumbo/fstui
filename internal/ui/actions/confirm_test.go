// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// fakeRunner counts runs instead of running anything.
type fakeRunner struct {
	mode    execute.Mode
	blocked error
	runs    int
}

func (f *fakeRunner) CurrentMode() execute.Mode      { return f.mode }
func (f *fakeRunner) Confirmable(*mutate.Plan) error { return f.blocked }
func (f *fakeRunner) Run(context.Context, *mutate.Plan) (execute.Report, error) {
	f.runs++
	return execute.Report{}, nil
}

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) wait(d time.Duration) { c.t = c.t.Add(d) }

func testPlan(op mutate.Op, targets ...string) *mutate.Plan {
	return &mutate.Plan{
		Op: op, Targets: targets, Summary: "do it",
		Steps: []execute.Argv{{Tool: execute.ZFS, Options: []string{"x"}, Operands: targets}},
	}
}

func press(t *testing.T, m ui.Modal, keys ...string) (ui.Modal, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		if m == nil {
			t.Fatalf("modal closed before %q", k)
		}
		msg := tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "backspace":
			msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
		}
		m, cmd = m.Update(msg)
	}
	return m, cmd
}

// finish runs the command a confirmation returned and feeds the result back.
func finish(t *testing.T, m ui.Modal, cmd tea.Cmd) ui.Modal {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run the plan")
	}
	m, _ = m.Update(cmd())
	return m
}

func TestLowTierConfirmsWithY(t *testing.T) {
	r, c := &fakeRunner{mode: execute.Write}, &clock{t: time.Unix(0, 0)}
	m, cmd := press(t, NewConfirm(testPlan(mutate.SnapshotCreate, "tank/a"), r, nil, c.now), "y")
	finish(t, m, cmd)
	if r.runs != 1 {
		t.Fatalf("runs = %d", r.runs)
	}
}

func TestMediumTierNeedsYThenEnterAfterGrace(t *testing.T) {
	r, c := &fakeRunner{mode: execute.Write}, &clock{t: time.Unix(0, 0)}
	var m ui.Modal = NewConfirm(testPlan(mutate.Unmount, "tank/a"), r, nil, c.now)
	// Keys during the grace period are dropped, and extend it.
	m, _ = press(t, m, "y", "enter")
	c.wait(400 * time.Millisecond)
	m, _ = press(t, m, "y", "enter")
	if r.runs != 0 || m.Busy() {
		t.Fatal("confirmed during the grace period")
	}
	c.wait(graceQuiet)
	// enter alone presses Cancel, which has the focus.
	if m2, _ := press(t, m, "enter"); m2 != nil {
		t.Fatal("enter with Cancel focused did not close")
	}
	m, cmd := press(t, m, "y", "enter")
	finish(t, m, cmd)
	if r.runs != 1 {
		t.Fatalf("runs = %d", r.runs)
	}
}

func TestGraceIsCapped(t *testing.T) {
	r, c := &fakeRunner{mode: execute.Write}, &clock{t: time.Unix(0, 0)}
	var m ui.Modal = NewConfirm(testPlan(mutate.Unmount, "tank/a"), r, nil, c.now)
	// A key every 300 ms keeps input from going quiet, but the grace
	// period ends 1.5 s after opening all the same.
	for range 5 {
		c.wait(300 * time.Millisecond)
		m, _ = press(t, m, "x")
	}
	c.wait(10 * time.Millisecond)
	m, cmd := press(t, m, "y", "enter")
	finish(t, m, cmd)
	if r.runs != 1 {
		t.Fatalf("runs = %d after the cap", r.runs)
	}
}

func TestHighTierNeedsTheExactName(t *testing.T) {
	r, c := &fakeRunner{mode: execute.Write}, &clock{t: time.Unix(0, 0)}
	var m ui.Modal = NewConfirm(testPlan(mutate.DatasetDestroy, "tank/a"), r, nil, c.now)
	c.wait(time.Second)
	m, _ = press(t, m, "enter", "y", "enter", "t", "a", "n", "k", "/", "b", "enter")
	if r.runs != 0 {
		t.Fatal("ran with a wrong name")
	}
	// y was typed as text: "ytank/b". Remove it and the b.
	m, _ = press(t, m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace",
		"t", "a", "n", "k", "/", "b")
	m, cmd := press(t, m, "backspace", "a", "enter")
	finish(t, m, cmd)
	if r.runs != 1 {
		t.Fatalf("runs = %d", r.runs)
	}
}

func TestBlockedPlanCannotBeConfirmed(t *testing.T) {
	r, c := &fakeRunner{mode: execute.ReadOnly, blocked: mutate.ErrReadOnly}, &clock{t: time.Unix(0, 0)}
	m, _ := press(t, NewConfirm(testPlan(mutate.SnapshotCreate, "tank/a"), r, nil, c.now), "y", "enter")
	if r.runs != 0 || m == nil {
		t.Fatalf("runs = %d, modal %v", r.runs, m)
	}
	if !strings.Contains(render(m), "read-only") {
		t.Errorf("render lacks the reason:\n%s", render(m))
	}
}

func TestEscCancelsInEveryTier(t *testing.T) {
	for _, op := range []mutate.Op{mutate.SnapshotCreate, mutate.Unmount, mutate.DatasetDestroy} {
		r, c := &fakeRunner{mode: execute.Write}, &clock{t: time.Unix(0, 0)}
		if m, _ := press(t, NewConfirm(testPlan(op, "tank/a"), r, nil, c.now), "esc"); m != nil {
			t.Errorf("%s: esc did not close", op)
		}
	}
}

func TestPreviewShowsCommandsAndDestroys(t *testing.T) {
	p := testPlan(mutate.DatasetDestroy, "tank/a")
	p.Preview.Destroys = []termtext.Text{termtext.New("tank/a@evil\x1b[2J")}
	p.Irreversible = []string{"Data in tank/a is gone."}
	out := render(NewConfirm(p, &fakeRunner{mode: execute.Write}, nil, time.Now))
	for _, want := range []string{"zfs x -- tank/a", "tank/a@evil", "Data in tank/a is gone.", "To proceed, type tank/a", "─do it"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tier") {
		t.Error("the dialog speaks of tiers, which mean nothing to the user")
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Error("render contains a raw escape from a name")
	}
}

func render(m ui.Modal) string {
	d := ui.Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.ASCII, nil)}
	return ansi.Strip(m.Render(d, 100, 30))
}

func TestBlockedReasonsAreSanitised(t *testing.T) {
	p := testPlan(mutate.Mount, "tank/a")
	p.Blocked = []string{"tank/a has mountpoint=/x\x1b]0;owned\x07"}
	r := &fakeRunner{mode: execute.Write}
	r.blocked = errors.New("cannot run: " + p.Blocked[0])
	out := NewConfirm(p, r, nil, time.Now).Render(ui.Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.ASCII, nil)}, 100, 30)
	if strings.ContainsAny(out, "\x07") || strings.Contains(out, "\x1b]") {
		t.Fatalf("raw escape in render: %q", out)
	}
}

// After ctrl+s the window says where the plan went, and stops offering
// to save it again.
func TestSavedPlanSaysWhere(t *testing.T) {
	r, c := &fakeRunner{mode: execute.ReadOnly, blocked: mutate.ErrReadOnly}, &clock{t: time.Unix(0, 0)}
	conf := NewConfirm(testPlan(mutate.SnapshotHold, "tank/a@1"), r, nil, c.now)
	conf.save = func(*mutate.Plan) (string, error) { return "9765b296208b0123456789", nil }
	if !strings.Contains(render(conf), "ctrl+s saves it for later, in Plans.") {
		t.Fatalf("no save hint:\n%s", render(conf))
	}
	conf.saveNow()
	out := render(conf)
	if !strings.Contains(out, "Saved in Plans as 9765b296208b.") || strings.Contains(out, "ctrl+s saves") {
		t.Errorf("after saving:\n%s", out)
	}
	for _, k := range conf.Keys() {
		if k.Help().Key == "ctrl+s" {
			t.Error("the key bar still offers to save")
		}
	}
}

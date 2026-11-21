// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// drain runs cmd and what it returns, feeding the messages to m, until
// nothing is left or a command takes longer than wait (streams and
// timers never end).
func drain(m tea.Model, cmd tea.Cmd, wait time.Duration, depth int) tea.Model {
	if cmd == nil || depth > 6 {
		return m
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(wait):
		return m
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = drain(m, c, wait, depth+1)
		}
		return m
	}
	m, next := m.Update(msg)
	return drain(m, next, wait, depth+1)
}

// fits draws m at w x h and reports a panic, more lines than h, or a
// line wider than w.
func fits(t *testing.T, m tea.Model, name string, w, h int) tea.Model {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panic: %v", name, r)
		}
	}()
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > h {
		t.Errorf("%s: %d lines", name, len(lines))
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("%s: line %d is %d cells", name, i, lw)
			break
		}
	}
	return m
}

// Every view, with the demo's data loaded, draws at every size from
// degenerate to wide without panicking, and never draws more lines or
// wider lines than the terminal has.
func TestEveryViewDrawsAtEverySize(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the demo")
	}
	cfg := config.Default()
	backends, err := zfsScreens(cfg)
	if err != nil {
		t.Fatal(err)
	}
	screens := append(backends.screens, ui.NewConfig(ui.NewSettings("", nil, cfg, nil, config.Load, nil), ui.NewNotifications("", cfg, func() (config.Secrets, []config.Problem) { return nil, nil }, nil, nil), ui.NewAlerts("", cfg, nil), ui.NewSchedules(nil, "", time.Minute, time.Now)))
	var m tea.Model = ui.New(ui.Session{Host: termtext.New("demo")}, screens, ui.Options{
		Profile: colorprofile.TrueColor, Mouse: true, Now: time.Now, StartView: poolsView,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = drain(m, m.Init(), 500*time.Millisecond, 0)

	sizes := [][2]int{{1, 1}, {2, 2}, {10, 3}, {20, 5}, {40, 10}, {79, 23}, {80, 24}, {100, 30}, {119, 40}, {120, 40}, {121, 41}, {160, 50}, {200, 60}, {300, 20}}
	for _, view := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "9"} {
		m, _ = m.Update(tea.KeyPressMsg{Code: rune(view[0]), Text: view})
		m = drain(m, func() tea.Msg { return nil }, 10*time.Millisecond, 0)
		for _, s := range sizes {
			m = fits(t, m, fmt.Sprintf("view %s at %dx%d", view, s[0], s[1]), s[0], s[1])
		}
	}
	// A pool's detail, opened, at the same sizes.
	m, _ = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = drain(m, cmd, 500*time.Millisecond, 0)
	for _, tab := range []int{0, 1, 2, 3, 4} {
		for _, s := range sizes {
			m = fits(t, m, fmt.Sprintf("pool detail tab %d at %dx%d", tab, s[0], s[1]), s[0], s[1])
		}
		m, cmd = m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
		m = drain(m, cmd, 500*time.Millisecond, 0)
	}
}

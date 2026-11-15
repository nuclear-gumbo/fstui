// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui/uitest"
)

// fakeScreen stands in for a real view (stand-ins only at the UI
// seam). It draws numbered lines, more than any terminal is tall, so the
// frame's clipping is exercised.
type fakeScreen struct {
	name   string
	nested bool
	status Status
	got    []tea.Msg
}

const fakeLines = 100

var fakeOpen = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open"))

func (f *fakeScreen) Init() tea.Cmd       { return nil }
func (f *fakeScreen) Name() string        { return f.name }
func (f *fakeScreen) Keys() []key.Binding { return []key.Binding{fakeOpen} }
func (f *fakeScreen) Nested() bool        { return f.nested }
func (f *fakeScreen) Status() Status      { return f.status }

func (f *fakeScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	f.got = append(f.got, msg)
	return f, nil
}

func (f *fakeScreen) Render(_ Display, width, _ int) string {
	lines := make([]string, fakeLines)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s line %d %s", f.name, i+1, strings.Repeat("x", width))
	}
	return strings.Join(lines, "\n")
}

var epoch = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func newModel(profile colorprofile.Profile, screens ...Screen) Model {
	s := Session{Host: termtext.New("host01"), Privilege: "root"}
	return New(s, screens, Options{Profile: profile, Now: func() time.Time { return epoch }})
}

func sized(m Model, w, h int) Model {
	out, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return out.(Model) //nolint:forcetypeassert // Update always returns Model
}

func press(t *testing.T, m Model, k string) (Model, tea.Cmd) {
	t.Helper()
	var msg tea.KeyPressMsg
	switch k {
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	if msg.String() != k {
		t.Fatalf("test key %q is %q", k, msg.String())
	}
	out, cmd := m.Update(msg)
	return out.(Model), cmd //nolint:forcetypeassert // Update always returns Model
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestLayoutSweep(t *testing.T) {
	scr := &fakeScreen{name: "Pools", status: Status{Refreshed: epoch.Add(-3 * time.Second), Figure: "2 pools, 1 DEGRADED"}}
	for _, w := range []int{80, 81, 100, 119, 120, 160, 199, 200, 250} {
		for _, h := range []int{24, 25, 40, 60} {
			checkFrame(t, sized(newModel(colorprofile.TrueColor, scr), w, h).View().Content, w, h)
		}
	}
}

// checkFrame asserts the layout invariants: exactly h lines of
// exactly w cells, both bars present, and the screen clipped to the body.
func checkFrame(t *testing.T, content string, w, h int) {
	t.Helper()
	lines := strings.Split(ansi.Strip(content), "\n")
	if len(lines) != h {
		t.Errorf("%dx%d: %d lines", w, h, len(lines))
		return
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw != w {
			t.Errorf("%dx%d: line %d is %d cells wide", w, h, i+1, lw)
		}
	}
	if !strings.Contains(lines[h-1], "READ-ONLY") || !strings.Contains(lines[h-2], "? help") {
		t.Errorf("%dx%d: bars missing:\n%s\n%s", w, h, lines[h-2], lines[h-1])
	}
	// The view bar, then the screen in a panel: top edge, h-frameLines-2 rows,
	// bottom.
	if !strings.HasPrefix(lines[0], " ¹pools") {
		t.Errorf("%dx%d: view bar: %q", w, h, lines[0])
	}
	if !strings.HasPrefix(lines[1], "╭─pools─") || !strings.HasPrefix(lines[h-3], "╰") {
		t.Errorf("%dx%d: panel edges:\n%s\n%s", w, h, lines[1], lines[h-3])
	}
	if !strings.Contains(lines[2], "Pools line 1 ") || !strings.Contains(lines[h-4], fmt.Sprintf("Pools line %d ", h-frameLines-2)) {
		t.Errorf("%dx%d: content not clipped to the panel:\n%s\n%s", w, h, lines[2], lines[h-4])
	}
}

func TestSizeNotice(t *testing.T) {
	for _, sz := range [][2]int{{79, 24}, {80, 23}, {40, 10}} {
		got := sized(newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"}), sz[0], sz[1]).View().Content
		want := fmt.Sprintf("Terminal is %dx%d. fstui needs at least 80x24.", sz[0], sz[1])
		if !strings.Contains(got, want) || strings.Contains(got, "READ-ONLY") {
			t.Errorf("%dx%d: got\n%s", sz[0], sz[1], got)
		}
	}
}

func TestStatusBarDropOrder(t *testing.T) {
	long := strings.Repeat("f", 30)
	scr := &fakeScreen{name: "Pools", status: Status{Refreshed: epoch.Add(-14 * time.Minute), Figure: long}}
	m := newModel(colorprofile.ASCII, scr)
	m.session.Host = termtext.New(strings.Repeat("h", 20))
	for _, tc := range []struct {
		width      int
		has, lacks []string
	}{
		{120, []string{"READ-ONLY", "root", "refreshed 14m ago", long}, nil},
		{80, []string{"READ-ONLY", "root", "refreshed 14m ago"}, []string{long}},
	} {
		m = sized(m, tc.width, MinHeight)
		got := m.statusBar(scr.Status())
		for _, s := range tc.has {
			if !strings.Contains(got, s) {
				t.Errorf("width %d: %q lacks %q", tc.width, got, s)
			}
		}
		for _, s := range tc.lacks {
			if strings.Contains(got, s) {
				t.Errorf("width %d: %q should have dropped %q", tc.width, got, s)
			}
		}
	}
	m.width = 30
	if got := m.statusBar(scr.Status()); !strings.HasSuffix(got, "READ-ONLY") || lipgloss.Width(got) > 30 {
		t.Errorf("narrow: %q, want the host shortened and the mode whole", got)
	}
}

func TestStaleDataShowsError(t *testing.T) {
	scr := &fakeScreen{name: "Pools", status: Status{
		Refreshed: epoch.Add(-2 * time.Minute),
		LastError: termtext.New("cannot open 'tank': no such pool\nsecond line"),
	}}
	m := sized(newModel(colorprofile.ASCII, scr), 120, MinHeight)
	if got := m.statusBar(scr.Status()); !strings.Contains(got, "stale 2m, last error: cannot open 'tank': no such pool") || strings.Contains(got, "second line") {
		t.Errorf("status = %q", got)
	}
}

func TestSuperuserMarker(t *testing.T) {
	m := sized(newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"}), 120, MinHeight)
	if strings.Contains(m.statusBar(Status{}), "warnings off") {
		t.Fatal("marker shown without superuser mode")
	}
	m.session.Superuser = true
	if !strings.Contains(m.statusBar(Status{}), "warnings off") {
		t.Fatal("superuser mode is not visible in the status bar")
	}
}

func TestHostileHostName(t *testing.T) {
	m := newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"})
	m.session.Host = termtext.New("evil\x1b[2J\x1b]0;pwned\x07")
	got := sized(m, 120, MinHeight).View().Content
	for _, hostile := range []string{"\x1b[2J", "\x1b]0;", "\x07"} {
		if strings.Contains(got, hostile) {
			t.Fatalf("%q from the host name reached the terminal: %q", hostile, got)
		}
	}
	if !strings.Contains(got, `evil\x1b[2J`) {
		t.Fatalf("host name not shown with visible escapes: %q", got)
	}
}

func TestKeys(t *testing.T) {
	a, b := &fakeScreen{name: "Pools"}, &fakeScreen{name: "Datasets"}
	m := sized(newModel(colorprofile.ASCII, a, b), 120, 40)

	m, _ = press(t, m, "2")
	if m.active != 1 {
		t.Fatalf("2 did not switch to the second screen")
	}
	m, _ = press(t, m, "9")
	if m.active != 1 {
		t.Fatalf("9 with two screens changed the screen to %d", m.active)
	}
	m, _ = press(t, m, "x")
	if len(b.got) == 0 || len(a.got) != 0 {
		t.Fatalf("an unbound key went to %d/%d messages (active/inactive)", len(b.got), len(a.got))
	}

	m, _ = press(t, m, "?")
	if !m.help || !strings.Contains(ansi.Strip(m.View().Content), "switch view") {
		t.Fatal("? did not open the help overlay with the global keys")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "enter  open") {
		t.Fatal("help overlay lacks the screen's keys")
	}
	m, cmd := press(t, m, "q")
	if m.help || isQuit(cmd) {
		t.Fatal("q in help should close the overlay, not quit")
	}

	b.nested = true
	m, cmd = press(t, m, "esc")
	if isQuit(cmd) {
		t.Fatal("esc in a drilled-in screen quit instead of going back")
	}
	b.nested = false
	if _, cmd = press(t, m, "q"); !isQuit(cmd) {
		t.Fatal("q at the top level did not quit")
	}
	if _, cmd = press(t, m, "ctrl+c"); !isQuit(cmd) {
		t.Fatal("ctrl+c did not quit")
	}
}

func TestDataMessagesReachEveryScreen(t *testing.T) {
	a, b := &fakeScreen{name: "Pools"}, &fakeScreen{name: "Datasets"}
	type loaded struct{}
	m := newModel(colorprofile.ASCII, a, b)
	m.Update(loaded{})
	if len(a.got) != 1 || len(b.got) != 1 {
		t.Fatalf("messages: %d, %d; want one each", len(a.got), len(b.got))
	}
}

func TestLightBackground(t *testing.T) {
	m := sized(newModel(colorprofile.TrueColor, &fakeScreen{name: "Pools"}), 120, MinHeight)
	dark := m.View().Content
	out, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#FFFFFF")})
	if light := out.(Model).View().Content; light == dark { //nolint:forcetypeassert // Update always returns Model
		t.Fatal("a light background did not change the palette")
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second: "0s", 3 * time.Second: "3s", 14 * time.Minute: "14m",
		2 * time.Hour: "2h", 5 * 24 * time.Hour: "5d",
	} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %s, want %s", d, got, want)
		}
	}
}

func TestGolden(t *testing.T) {
	scr := &fakeScreen{name: "Pools", status: Status{Refreshed: epoch.Add(-3 * time.Second), Figure: "2 pools, 1 DEGRADED"}}
	for _, sz := range [][2]int{{80, 24}, {120, 40}, {200, 60}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			uitest.Golden(t, sized(newModel(colorprofile.ASCII, scr), sz[0], sz[1]).View().Content)
		})
	}
	t.Run("truecolor_help", func(t *testing.T) {
		m, _ := press(t, sized(newModel(colorprofile.TrueColor, scr), 80, MinHeight), "?")
		uitest.Golden(t, m.View().Content)
	})
}

func TestNotices(t *testing.T) {
	m := newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"})
	m.session.Notices = []string{"/etc/fstui/config.yaml: ignored: writable by group", "second"}
	m = sized(m, 120, MinHeight)
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "ignored: writable by group") || !strings.Contains(out, "and 1 more; esc dismisses") {
		t.Fatalf("notice missing:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != MinHeight || !strings.Contains(lines[MinHeight-1], "READ-ONLY") || !strings.Contains(lines[MinHeight-2], "? help") {
		t.Fatalf("frame broken by the notice:\n%s", out)
	}
	m, cmd := press(t, m, "esc")
	if isQuit(cmd) || len(m.session.Notices) != 0 {
		t.Fatal("esc should dismiss the notice, not quit")
	}
}

type activatedScreen struct {
	fakeScreen
	activations, deactivations int
}

func (a *activatedScreen) Activate() tea.Cmd {
	a.activations++
	return nil
}

func (a *activatedScreen) Deactivate() tea.Cmd {
	a.deactivations++
	return nil
}

func TestActivateOnSwitch(t *testing.T) {
	a := &activatedScreen{fakeScreen: fakeScreen{name: "Snapshots"}}
	m := sized(newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"}, a), 120, 40)
	m, _ = press(t, m, "2")
	m, _ = press(t, m, "2") // already active: no second read
	if a.activations != 1 {
		t.Fatalf("activations = %d, want 1", a.activations)
	}
	if a.deactivations != 0 {
		t.Fatal("deactivated while shown")
	}
	press(t, m, "1")
	if a.activations != 1 || a.deactivations != 1 {
		t.Fatalf("leaving the screen: activations %d, deactivations %d", a.activations, a.deactivations)
	}
}

// The status bar's data age must advance between reads: the root keeps a
// one-second clock that redraws the frame ("refreshed" always said 0s).
func TestClockKeepsTicking(t *testing.T) {
	m := newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"})
	_, cmd := m.Update(clockMsg{})
	if cmd == nil {
		t.Fatal("the clock stopped after one tick")
	}
}

type mouseScreen struct {
	fakeScreen
	got []tea.Msg
}

func (s *mouseScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	s.got = append(s.got, msg)
	return s, nil
}

func TestMouseTranslation(t *testing.T) {
	a, b := &mouseScreen{fakeScreen: fakeScreen{name: "Pools"}}, &mouseScreen{fakeScreen: fakeScreen{name: "Datasets"}}
	m := sized(New(Session{Host: termtext.New("h")}, []Screen{a, b}, Options{Profile: colorprofile.ASCII, Mouse: true, Now: func() time.Time { return epoch }}), 120, 40)
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse reporting is off although enabled")
	}
	m.Update(tea.MouseWheelMsg{X: 3, Y: 5, Button: tea.MouseWheelDown})
	m.Update(tea.MouseClickMsg{X: 7, Y: 2, Button: tea.MouseLeft})  // first row inside the panel
	m.Update(tea.MouseClickMsg{X: 7, Y: 1, Button: tea.MouseLeft})  // panel border: ignored
	m.Update(tea.MouseClickMsg{X: 0, Y: 5, Button: tea.MouseLeft})  // panel border: ignored
	m.Update(tea.MouseClickMsg{X: 7, Y: 39, Button: tea.MouseLeft}) // status bar: ignored
	m.Update(tea.MouseClickMsg{X: 7, Y: 5, Button: tea.MouseRight}) // not a left click
	want := []tea.Msg{ScrollMsg{Delta: 1}, ClickMsg{X: 6, Y: 0}}
	if len(a.got) != len(want) || a.got[0] != want[0] || a.got[1] != want[1] {
		t.Fatalf("active screen got %v, want %v", a.got, want)
	}
	if len(b.got) != 0 {
		t.Fatal("an inactive screen got mouse input")
	}
	// The view bar reads " ¹pools  ²datasets"; a click on a name opens it.
	if lines := strings.Split(ansi.Strip(m.View().Content), "\n"); !strings.HasPrefix(lines[0], " ¹pools  ²datasets") {
		t.Fatalf("view bar: %q", lines[0])
	}
	next, _ := m.Update(tea.MouseClickMsg{X: 10, Y: 0, Button: tea.MouseLeft})
	if nm, ok := next.(Model); !ok || nm.active != 1 || len(a.got) != len(want) {
		t.Fatal("a click on datasets in the view bar did not switch views")
	}
	off := sized(New(Session{}, []Screen{a}, Options{Profile: colorprofile.ASCII, Now: func() time.Time { return epoch }}), 120, 40)
	if off.View().MouseMode != tea.MouseModeNone {
		t.Fatal("mouse reporting on although disabled")
	}
}

// The help hint survives a narrow terminal; the screen's hints are
// dropped whole from the end instead of being cut.
func TestHintBarKeepsHelp(t *testing.T) {
	m := newModel(colorprofile.ASCII, &fakeScreen{name: "Pools"})
	m.width = 40
	got := ansi.Strip(m.hintBar([]key.Binding{
		key.NewBinding(key.WithKeys("l"), key.WithHelp("l/right", "next tab")),
		key.NewBinding(key.WithKeys("h"), key.WithHelp("h/left", "previous tab")),
		key.NewBinding(key.WithKeys("j"), key.WithHelp("j/down", "scroll")),
	}))
	if got != "l/right next tab  h/left previous tab  ? help" && got != "l/right next tab  ? help" {
		t.Errorf("hint bar at 40 columns: %q", got)
	}
	if lipgloss.Width(got) > 40 {
		t.Errorf("hint bar wider than 40: %q", got)
	}
}

// fstui --dashboard starts on view 10 (the 0 key).
func TestStartView(t *testing.T) {
	a, b := &fakeScreen{name: "Pools"}, &numberedScreen{fakeScreen: fakeScreen{name: "Dashboard"}, n: 10}
	m := New(Session{}, []Screen{a, b}, Options{Profile: colorprofile.ASCII, StartView: 10, Now: func() time.Time { return epoch }})
	if m.active != 1 {
		t.Fatalf("active %d", m.active)
	}
	if m = New(Session{}, []Screen{a, b}, Options{Profile: colorprofile.ASCII, StartView: 5, Now: func() time.Time { return epoch }}); m.active != 0 {
		t.Fatalf("a view that does not exist: active %d", m.active)
	}
}

type numberedScreen struct {
	fakeScreen
	n int
}

func (s *numberedScreen) Number() int { return s.n }

// In ASCII mode at 80 columns every view stays in the bar, and nothing
// on screen uses the "…" the Linux console may not have.
func TestASCIIViewBarAndEllipsis(t *testing.T) {
	names := []string{"Pools", "Datasets", "Snapshots", "Volumes", "ARC", "I/O", "Events", "Dashboard"}
	var screens []Screen
	for _, n := range names {
		screens = append(screens, &fakeScreen{name: n}) // its lines are wider than the panel
	}
	m := sized(New(Session{}, screens, Options{Profile: colorprofile.ASCII, ASCII: true, Now: func() time.Time { return epoch }}), 80, 24)
	out := ansi.Strip(m.View().Content)
	bar := strings.Split(out, "\n")[0]
	if !strings.Contains(bar, "8:dash") || lipgloss.Width(bar) > 80 || strings.Contains(bar, "~") {
		t.Errorf("view bar: %q", bar)
	}
	if strings.Contains(out, "…") || !strings.Contains(out, "~") {
		t.Errorf("ellipsis in ASCII mode:\n%s", out)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: strings.Index(bar, "8:dash"), Y: 0, Button: tea.MouseLeft})
	if nm, ok := next.(Model); !ok || nm.active != 7 {
		t.Error("a click on a shortened name did not open its view")
	}
}

func TestNaturalScrollingReversesTheWheel(t *testing.T) {
	up := tea.MouseWheelMsg{Button: tea.MouseWheelUp}
	if got := wheel(up, false); got != (ScrollMsg{Delta: -1}) {
		t.Errorf("wheel up = %v, want up one row", got)
	}
	if got := wheel(up, true); got != (ScrollMsg{Delta: 1}) {
		t.Errorf("natural wheel up = %v, want down one row", got)
	}
	if got := wheel(tea.MouseWheelMsg{Button: tea.MouseWheelLeft}, true); got != nil {
		t.Errorf("sideways wheel = %v, want nothing", got)
	}
}

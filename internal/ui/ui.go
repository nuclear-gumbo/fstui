// SPDX-License-Identifier: GPL-3.0-or-later

// Package ui is fstui's terminal interface: the root model routes messages
// to the active screen and draws the frame around it, the title line, the
// key hint bar and the status bar.
//
// Screens own their state and their keys. The root owns only what every
// screen shares: the terminal size, the theme, the help overlay and the
// session facts shown in the status bar.
package ui

import (
	"sync/atomic"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Minimum terminal size.
const (
	MinWidth  = 80
	MinHeight = 24
)

// frameLines are the lines the frame takes from the content area: the
// view bar above it, the key hint bar and the status bar below.
const frameLines = 3

// maxScreens is reachable with the number keys 1-9 and 0.
const maxScreens = 10

// Display holds the presentation settings every screen renders with.
type Display struct {
	Palette theme.Palette
	Exact   bool // exact byte counts instead of IEC units (b toggles)
	ASCII   bool // no box-drawing or block characters
	// CursorOff is the blink: a field taking typed text draws its cursor
	// only while this is false (Cursor).
	CursorOff bool
}

// keyboardEnhanced is set when the terminal tells modified keys apart
// (the kitty keyboard protocol), so ctrl+enter is not read as enter.
var keyboardEnhanced atomic.Bool

// SkipKey names the key that goes straight to the confirmation from a
// form: ctrl+enter where the terminal reports it, else alt+enter, which
// every terminal sends as escape then enter. Both work either way.
func SkipKey() string {
	if keyboardEnhanced.Load() {
		return "ctrl+enter"
	}
	return "alt+enter"
}

// Cursor is the cursor of a field taking typed text. It is a block (an
// underscore in ASCII mode) and a space while blinked off.
func Cursor(d Display) string {
	switch {
	case d.CursorOff:
		return " "
	case d.ASCII:
		return "_"
	}
	return "█"
}

// blinkMsg turns a typing cursor on or off.
type blinkMsg struct{}

// blinkInterval is the usual terminal cursor blink rate.
const blinkInterval = 530 * time.Millisecond

func blink() tea.Cmd {
	return tea.Tick(blinkInterval, func(time.Time) tea.Msg { return blinkMsg{} })
}

// Screen is a top-level view.
type Screen interface {
	// Init starts the screen's first load.
	Init() tea.Cmd
	// Name is the title shown in the title line, e.g. "Pools".
	Name() string
	// Keys lists the screen's bindings, most relevant first. The hint bar
	// shows as many as fit; the help overlay shows all of them.
	Keys() []key.Binding
	// Nested reports whether the screen is drilled in, so that esc and q
	// go back inside it instead of quitting.
	Nested() bool
	Update(msg tea.Msg) (Screen, tea.Cmd)
	// Render draws the screen in exactly width x height cells.
	Render(d Display, width, height int) string
	// Status reports the age of the screen's data for the status bar.
	Status() Status
}

// Typing is implemented by a screen that can be taking typed text, such
// as a number in the Config view. While Typing reports true, every key but quit
// goes to the screen: digits, b, q and ? are text there, not view
// switches or toggles.
type Typing interface {
	Typing() bool
}

// ScrollMsg is the mouse wheel, sent to the active screen: Delta is -1
// for up and 1 for down. Lists move their selection, other views scroll.
type ScrollMsg struct{ Delta int }

// ClickMsg is a left click inside the active screen's area, in the
// screen's own coordinates: row 0 is the first line the screen drew.
type ClickMsg struct{ X, Y int }

// Claimer is implemented by screens that use a key the root otherwise
// handles: the Config view's Notifications tab adds a channel with a.
type Claimer interface {
	Claims(msg tea.KeyPressMsg) bool
}

func (m Model) claimed(msg tea.KeyPressMsg) bool {
	if len(m.screens) == 0 {
		return false
	}
	c, ok := m.screens[m.active].(Claimer)
	return ok && c.Claims(msg)
}

// Framed is implemented by screens that draw their own panels, such as
// list and detail side by side; the root frames all others in one panel
// titled with the view's name and number, and gives them the inner size.
type Framed interface {
	Framed() bool
}

func framed(s Screen) bool {
	f, ok := s.(Framed)
	return ok && f.Framed()
}

// number is the view number of screen i: the one it claims, or its
// position.
func (m Model) number(i int) int {
	if n, ok := m.screens[i].(Numbered); ok {
		return n.Number()
	}
	return i + 1
}

// Numbered is implemented by screens with a fixed view number, such as
// Settings on 9; others take their position.
type Numbered interface {
	Number() int // 1 to 9, or 10 for the 0 key
}

// ConfigMsg carries settings saved in the Config view. The root applies
// the display settings; screens take what concerns them.
type ConfigMsg struct {
	Config config.Config
	Theme  theme.Theme
	ASCII  bool // ascii: resolved for this terminal
}

// SwitchViewMsg asks the root to show view View, as its number key would;
// the dashboard sends it to open what is selected in a panel.
type SwitchViewMsg struct{ View int }

// ThemeMsg shows a theme without saving it: Settings previews the theme
// the user is choosing, and restores the saved one when the choice is
// discarded or left unsaved.
type ThemeMsg struct{ Theme theme.Theme }

// Activator is implemented by screens that read only when they are shown:
// sources too large to read at startup or on a timer, such as every
// snapshot on the system.
type Activator interface {
	Activate() tea.Cmd
}

// Deactivator is implemented by screens that read faster while shown and
// slow down when the user switches away.
type Deactivator interface {
	Deactivate() tea.Cmd
}

// Status is a screen's contribution to the status bar.
type Status struct {
	Refreshed time.Time     // zero until the first load completes
	LastError termtext.Text // set while the data is stale because reads fail
	Figure    string        // one view-relevant figure, e.g. "2 pools, 1 DEGRADED"
	Static    bool          // the view reads nothing from the system: no refresh age
}

// Session holds the facts about this fstui process that the status bar
// shows on every screen.
type Session struct {
	Host      termtext.Text
	Privilege string // "root", or "unprivileged (alice)"
	Label     string // shown before the mode, e.g. DEMO; empty for none
	Mode      string // shown after the host; empty means READ-ONLY
	Superuser bool   // advisory warnings hidden
	// Notices are problems found before the UI started, such as config
	// files that were ignored. They show above the hint bar until esc.
	Notices []string
}

// Options are the presentation settings the root starts with.
type Options struct {
	Theme     theme.Theme
	Profile   colorprofile.Profile
	Overrides map[theme.Token]theme.Override
	Exact     bool
	ASCII     bool
	Mouse     bool             // report clicks and the wheel (config: mouse)
	Natural   bool             // natural scrolling: the wheel moves the content (config: natural_scrolling)
	Now       func() time.Time // clock for data ages
	StartView int              // view number to show first (fstui --dashboard: 10); 0 for the first
	NoBlink   bool             // a typing cursor does not blink (config: ui.cursor_blink)
	Actions   Actions          // the write module's changes; nil without it
	Hosts     *HostLister      // the other hosts H lists; nil without the remote module
	Config    config.Config    // the settings fstui started with; saved ones replace them
}

// Model is the root Bubble Tea model.
type Model struct {
	session Session
	screens []Screen
	active  int
	width   int
	height  int
	help    bool
	opts    Options
	display Display
	dark    bool // the terminal's background, once it has answered
	modal   Modal
	next    *HostEntry // the host to show next, once the program ends
}

// switchHost ends the program so that it starts again on the chosen
// host, unless a change is running.
func (m Model) switchHost(msg SwitchHostMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil && m.modal.Busy() {
		return m, nil
	}
	m.next = &msg.Host
	return m, tea.Quit
}

// Next is the host the user chose in the host list, if the program
// ended for that.
func (m Model) Next() (HostEntry, bool) {
	if m.next == nil {
		return HostEntry{}, false
	}
	return *m.next, true
}

// New returns the root model. Until the terminal reports its background,
// dark values are used.
func New(s Session, screens []Screen, o Options) Model {
	if len(screens) > maxScreens {
		screens = screens[:maxScreens]
	}
	m := Model{
		session: s, screens: screens, opts: o, dark: true,
		display: Display{Palette: theme.Resolve(o.Theme, true, o.Profile, o.Overrides), Exact: o.Exact, ASCII: o.ASCII},
	}
	if o.StartView > 0 {
		if i := m.screenFor(o.StartView); i >= 0 {
			m.active = i
		}
	}
	return m
}

// Init asks the terminal for its background colour and starts every
// screen's first load.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, clock(), blink()}
	for _, s := range m.screens {
		cmds = append(cmds, s.Init())
	}
	if len(m.screens) > 0 {
		if a, ok := m.screens[m.active].(Activator); ok {
			cmds = append(cmds, a.Activate())
		}
	}
	return tea.Batch(cmds...)
}

// clockMsg redraws the frame once a second, so data ages in the status
// bar ("refreshed 3s ago") advance between reads.
type clockMsg struct{}

const clockInterval = time.Second

func clock() tea.Cmd {
	return tea.Tick(clockInterval, func(time.Time) tea.Msg { return clockMsg{} })
}

// Global bindings. Screen-specific keys live with screens.
var (
	keyView = key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9", "0"), key.WithHelp("1-9, 0", "switch view"))
)

func globalKeys() []key.Binding {
	return []key.Binding{keyView, keys.Get(keys.Bytes), keys.Get(keys.Help), keys.Get(keys.Back), keys.Get(keys.Quit)}
}

// blinkCursor blinks only while something takes typed text; otherwise
// the cursor stays on, so it shows at once when typing starts.
func (m Model) blinkCursor() Model {
	m.display.CursorOff = !m.display.CursorOff && (m.modal != nil || m.typing()) && !m.opts.NoBlink
	return m
}

// Update routes a message. Keys go to the help overlay when it is open,
// then to the global bindings, then to the active screen. Other messages
// go to every screen, because results of reads started by a screen
// arrive here whether or not it is still active.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyboardEnhancementsMsg:
		keyboardEnhanced.Store(msg.SupportsKeyDisambiguation())
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case clockMsg:
		return m, clock()
	case blinkMsg:
		return m.blinkCursor(), blink()
	case ConfigMsg:
		m.applyConfig(msg)
	case SwitchViewMsg:
		if i := m.screenFor(msg.View); i >= 0 && i != m.active {
			return m.activate(i)
		}
		return m, nil
	case OpenModalMsg:
		m.modal = msg.Modal
		return m, nil
	case SwitchHostMsg:
		return m.switchHost(msg)
	case ModeMsg:
		m.session.Mode = msg.Mode
		return m, nil
	case ThemeMsg:
		m.opts.Theme = msg.Theme
		return m.repaint(), nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		return m.repaint(), nil
	case tea.KeyPressMsg:
		return m.keyPress(msg)
	case tea.PasteMsg:
		return m.paste(msg)
	case OpenChangesMsg:
		return m.openChanges()
	case tea.MouseWheelMsg:
		if m.modal != nil {
			return m, nil
		}
		return m.toScreen(wheel(msg, m.opts.Natural))
	case tea.MouseClickMsg:
		if m.modal != nil {
			return m, nil
		}
		return m.click(msg)
	}
	return m.broadcast(msg)
}

// broadcast gives a message to the modal and to every screen: replies
// to reads and ticks find their screen this way.
func (m Model) broadcast(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0, len(m.screens)+1)
	if m.modal != nil {
		var cmd tea.Cmd
		m.modal, cmd = m.modal.Update(msg)
		cmds = append(cmds, cmd)
	}
	for i, s := range m.screens {
		var cmd tea.Cmd
		m.screens[i], cmd = s.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Get(keys.Quit)):
		return m, tea.Quit
	case m.typing():
		var cmd tea.Cmd
		m.screens[m.active], cmd = m.screens[m.active].Update(msg)
		return m, cmd
	case len(m.session.Notices) > 0 && key.Matches(msg, keys.Get(keys.Back)):
		m.session.Notices = nil
		return m, nil
	case m.help:
		if key.Matches(msg, keys.Get(keys.Help), keys.Get(keys.Back)) {
			m.help = false
		}
		return m, nil
	case key.Matches(msg, keys.Get(keys.Help)):
		m.help = true
		return m, nil
	case key.Matches(msg, keys.Get(keys.Bytes)):
		m.display.Exact = !m.display.Exact
		return m, nil
	case m.opts.Hosts != nil && key.Matches(msg, keys.Get(keys.Hosts)):
		modal, cmd := openHosts(*m.opts.Hosts, m.opts.Config)
		m.modal = modal
		return m, cmd
	case m.claimed(msg):
	case key.Matches(msg, keys.Get(keys.Actions), keys.Get(keys.WriteMode), keys.Get(keys.NewSnapshot)):
		if next, cmd, ok := m.actionKey(msg); ok {
			return next, cmd
		}
	case key.Matches(msg, keyView):
		if i := m.screenFor(viewIndex(msg.String()) + 1); i >= 0 && i != m.active {
			return m.activate(i)
		}
		return m, nil
	case key.Matches(msg, keys.Get(keys.Back)) && (len(m.screens) == 0 || !m.screens[m.active].Nested()):
		return m, tea.Quit
	case len(m.screens) == 0:
		return m, nil
	}
	var cmd tea.Cmd
	m.screens[m.active], cmd = m.screens[m.active].Update(msg)
	return m, cmd
}

// click handles a left click: on the view bar it switches views; in the
// content it goes to the active screen, in the screen's coordinates.
// Clicks on the bars below the content go nowhere.
func (m Model) click(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	body := m.height - frameLines - len(m.noticeLines())
	if msg.Button != tea.MouseLeft || m.help || len(m.screens) == 0 {
		return m, nil
	}
	if msg.Y == 0 {
		if i := m.viewAt(msg.X); i >= 0 && i != m.active {
			return m.activate(i)
		}
		return m, nil
	}
	x, y := msg.X, msg.Y-1 // below the view bar
	if y >= body {
		return m, nil
	}
	// A screen the root frames gets coordinates inside the border;
	// clicks on the border itself go nowhere.
	if !framed(m.screens[m.active]) {
		if x < 1 || y < 1 || x >= m.width-1 || y >= body-1 {
			return m, nil
		}
		x, y = x-1, y-1
	}
	return m.toScreen(ClickMsg{X: x, Y: y})
}

// activate makes screen i the active one, telling the screen it leaves
// and the one it shows.
func (m Model) activate(i int) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if d, ok := m.screens[m.active].(Deactivator); ok {
		cmds = append(cmds, d.Deactivate())
	}
	m.active = i
	if a, ok := m.screens[i].(Activator); ok {
		cmds = append(cmds, a.Activate())
	}
	return m, tea.Batch(cmds...)
}

// typing reports whether the active screen is taking typed text.
func (m Model) typing() bool {
	if m.help || len(m.screens) == 0 {
		return false
	}
	t, ok := m.screens[m.active].(Typing)
	return ok && t.Typing()
}

// wheel turns vertical wheel motion into a ScrollMsg; sideways wheels
// are ignored.
func wheel(msg tea.MouseWheelMsg, natural bool) tea.Msg {
	d := 0
	switch msg.Button { //nolint:exhaustive // sideways wheels do nothing
	case tea.MouseWheelUp:
		d = -1
	case tea.MouseWheelDown:
		d = 1
	default:
		return nil
	}
	if natural {
		d = -d
	}
	return ScrollMsg{Delta: d}
}

// toScreen hands a mouse message to the active screen only.
func (m Model) toScreen(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg == nil || len(m.screens) == 0 {
		return m, nil
	}
	var cmd tea.Cmd
	m.screens[m.active], cmd = m.screens[m.active].Update(msg)
	return m, cmd
}

// screenFor returns the screen shown by view number n: a screen claiming
// n, or else the screen in position n; -1 if none.
func (m Model) screenFor(n int) int {
	for i, s := range m.screens {
		if num, ok := s.(Numbered); ok && num.Number() == n {
			return i
		}
	}
	if i := n - 1; i < len(m.screens) {
		if _, ok := m.screens[i].(Numbered); !ok {
			return i
		}
	}
	return -1
}

func (m *Model) applyConfig(msg ConfigMsg) {
	c := msg.Config
	m.opts.Config = c
	m.opts.Theme, m.opts.Overrides = msg.Theme, c.Colors
	m.opts.Mouse, m.opts.Natural, m.opts.NoBlink = c.Mouse, c.NaturalScrolling, !c.UI.CursorBlink
	m.display.Exact = c.Units == config.Exact
	m.display.ASCII = msg.ASCII
	m.display.Palette = theme.Resolve(msg.Theme, m.dark, m.opts.Profile, c.Colors)
	m.session.Superuser = c.Superuser
}

// viewIndex maps "1".."9" to 0..8 and "0" to 9.
func viewIndex(k string) int {
	if k == "0" {
		return maxScreens - 1
	}
	return int(k[0] - '1')
}

// View draws the frame and the active screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	if m.opts.Mouse {
		// Clicks and the wheel; most terminals still select text with
		// shift held down.
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// paste types pasted text into the field that has focus, one character
// at a time, as if typed: each field keeps its own limits and the
// characters it accepts, and a secret stays hidden. Line breaks and other
// control characters are left out, so a password manager's trailing
// newline cannot finish a form. Text pasted where nothing takes typing is
// dropped: a letter there would be a command.
func (m Model) paste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if !m.pasteTarget() {
		return m, nil
	}
	var cmds []tea.Cmd
	var next tea.Model = m
	for _, r := range msg.Content {
		if unicode.IsControl(r) {
			continue
		}
		var cmd tea.Cmd
		next, cmd = next.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		cmds = append(cmds, cmd)
	}
	return next, tea.Batch(cmds...)
}

// pasteTarget reports whether a field takes typed text now: in the modal
// shown, or else the active screen.
func (m Model) pasteTarget() bool {
	if m.modal != nil {
		t, ok := m.modal.(Typing)
		return ok && t.Typing()
	}
	return m.typing()
}

// keyPress sends a key to the modal shown, or else the view's keys.
func (m Model) keyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m.modalKey(msg)
	}
	return m.key(msg)
}

// openChanges opens the change menu on the active view's selection, as
// the a key does.
func (m Model) openChanges() (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m, nil
	}
	if m.opts.Actions == nil || len(m.screens) == 0 {
		return m, nil
	}
	var cmd tea.Cmd
	if t, ok := m.selection(); ok {
		m.modal, cmd = m.opts.Actions.Menu(t)
	}
	if m.modal == nil {
		m.modal = m.noChanges()
	}
	return m, cmd
}

// repaint resolves the palette again after the theme or the terminal's
// background changed.
func (m Model) repaint() Model {
	m.display.Palette = theme.Resolve(m.opts.Theme, m.dark, m.opts.Profile, m.opts.Overrides)
	return m
}

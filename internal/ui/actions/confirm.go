// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

// Package actions is the write module's user interface: the menu of
// changes for a selection, the form for their parameters, the preview
// and confirmation and write mode.
package actions

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Grace period for medium and high tiers:
// keys are ignored until input has been quiet this long, but no longer
// than graceCap after the modal opened, so a key pressed for the
// previous screen cannot confirm.
const (
	graceQuiet = 425 * time.Millisecond
	graceCap   = 1500 * time.Millisecond
)

// Runner checks and runs a confirmed plan; *mutate.Pipeline is one.
type Runner interface {
	CurrentMode() execute.Mode
	Confirmable(p *mutate.Plan) error
	Run(ctx context.Context, p *mutate.Plan) (execute.Report, error)
}

// Rebuild builds the plan again from the current state, after the
// re-check found a change.
type Rebuild func(ctx context.Context) (*mutate.Plan, error)

type stage int

const (
	reviewing stage = iota
	running
	finished
)

// Confirm previews a plan and asks for its tier's confirmation.
type Confirm struct {
	plan    *mutate.Plan
	tier    mutate.Tier
	run     Runner
	rebuild Rebuild
	now     func() time.Time
	opened  time.Time
	armed   time.Time // keys before this are dropped
	stage   stage
	confirm bool   // medium tier: Confirm has the focus (Cancel has it first)
	typed   string // high tier
	scroll  int
	save    func(*mutate.Plan) (string, error) // nil: the plan cannot be saved
	saved   string                             // SHA-256 once saved, or why saving failed
	report  execute.Report
	err     error
	changed bool // the run stopped because the state changed
}

// NewConfirm opens the preview of p.
func NewConfirm(p *mutate.Plan, run Runner, rebuild Rebuild, now func() time.Time) *Confirm {
	c := &Confirm{plan: p, tier: mutate.TierOf(p), run: run, rebuild: rebuild, now: now, opened: now()}
	if c.tier > mutate.Low {
		c.armed = c.opened.Add(graceQuiet)
	}
	return c
}

// ranMsg and rebuiltMsg carry results back to the modal that started
// them, matched by plan.
type ranMsg struct {
	plan   *mutate.Plan
	report execute.Report
	err    error
}

type rebuiltMsg struct {
	was  *mutate.Plan
	plan *mutate.Plan
	err  error
}

// Busy implements ui.Modal.
func (c *Confirm) Busy() bool { return c.stage == running }

// Keys implements ui.Modal.
func (c *Confirm) Keys() []key.Binding {
	var out []key.Binding
	switch {
	case c.stage == finished && c.changed && c.rebuild != nil:
		return []key.Binding{keyRebuild, keys.As(keys.Back, "close")}
	case c.stage != reviewing:
		return []key.Binding{keys.As(keys.Back, "close")}
	case c.blocked() != nil:
		out = []key.Binding{keys.As(keys.Back, "close"), keyScroll}
	case c.tier == mutate.High:
		out = []key.Binding{keyTypeName, keyEnterConfirm, keyEscCancel}
	case c.tier == mutate.Medium:
		arm := key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "select "+c.verb()))
		out = []key.Binding{arm, keyEnterPress, keyFocus, keys.As(keys.Cancel, "cancel"), keyScroll}
	default:
		out = []key.Binding{keys.As(keys.Confirm, strings.ToLower(c.verb())), keys.As(keys.Cancel, "cancel"), keyScroll}
	}
	if c.save != nil && len(c.plan.Blocked) == 0 && c.saved == "" {
		out = append(out, keySave)
	}
	return out
}

var (
	keySave         = key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save for later"))
	keyRebuild      = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "look again"))
	keyScroll       = key.NewBinding(key.WithKeys("up", "down", "pgup", "pgdown"), key.WithHelp("↑/↓", "scroll"))
	keyTypeName     = key.NewBinding(key.WithKeys(""), key.WithHelp("type", "the words shown"))
	keyEnterConfirm = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "proceed"))
	keyEnterPress   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "press the button"))
	keyEscCancel    = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	keyFocus        = key.NewBinding(key.WithKeys("tab", "left", "right", "h", "l"), key.WithHelp("tab", "switch button"))
)

// blocked is why the plan cannot be confirmed, or nil.
func (c *Confirm) blocked() error { return c.run.Confirmable(c.plan) }

// Update implements ui.Modal.
func (c *Confirm) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case ranMsg:
		if msg.plan == c.plan {
			c.stage, c.report, c.err = finished, msg.report, msg.err
			var sc *mutate.StateChangedError
			c.changed = errors.As(msg.err, &sc)
			word := modeWord(c.run.CurrentMode())
			if msg.err == nil && len(c.plan.Steps) == 0 {
				// Nothing to report, such as switching to write mode: the
				// status bar shows the result.
				return nil, func() tea.Msg { return ui.ModeMsg{Mode: word} }
			}
			return c, tea.Batch(
				func() tea.Msg { return ui.ChangedMsg{} },
				func() tea.Msg { return ui.ModeMsg{Mode: word} },
			)
		}
	case rebuiltMsg:
		if msg.was == c.plan {
			if msg.err != nil {
				c.err = msg.err
				return c, nil
			}
			return NewConfirm(msg.plan, c.run, c.rebuild, c.now), nil
		}
	case tea.KeyPressMsg:
		m, cmd := c.key(msg)
		if m == nil && c.stage != running {
			clear(c.plan.Stdin) // closed without running: drop the passphrase now
		}
		return m, cmd
	}
	return c, nil
}

func (c *Confirm) key(msg tea.KeyPressMsg) (ui.Modal, tea.Cmd) {
	k := msg.String()
	switch c.stage {
	case running:
		return c, nil
	case finished:
		if c.changed && c.rebuild != nil && k == "r" {
			return c, c.startRebuild()
		}
		if k == "esc" || k == "enter" || k == "q" || k == "n" {
			return nil, nil
		}
		return c, nil
	case reviewing:
	}
	if k == "esc" {
		return nil, nil
	}
	if k == "ctrl+s" {
		c.saveNow()
		return c, nil
	}
	if now := c.now(); now.Before(c.armed) {
		c.armed = minTime(now.Add(graceQuiet), c.opened.Add(graceCap))
		return c, nil
	}
	if c.scrollKey(k) {
		return c, nil
	}
	if c.blocked() != nil {
		if k == "q" || k == "n" {
			return nil, nil
		}
		return c, nil
	}
	switch c.tier {
	case mutate.Low:
		return c.lowKey(k)
	case mutate.Medium:
		return c.mediumKey(k)
	case mutate.High:
		return c.highKey(msg)
	}
	return c, nil
}

func (c *Confirm) scrollKey(k string) bool {
	switch k {
	case "up", "pgup":
		c.scroll = max(c.scroll-1, 0)
	case "down", "pgdown":
		c.scroll++
	default:
		return false
	}
	return true
}

func (c *Confirm) lowKey(k string) (ui.Modal, tea.Cmd) {
	switch k {
	case "y", "enter":
		return c, c.start()
	case "n", "q":
		return nil, nil
	}
	return c, nil
}

// mediumKey: Cancel has the focus first; y moves it to Confirm, and
// enter presses the focused button. Nothing confirms in one key.
func (c *Confirm) mediumKey(k string) (ui.Modal, tea.Cmd) {
	switch k {
	case "y":
		c.confirm = true
	case "tab", "left", "right", "h", "l":
		c.confirm = !c.confirm
	case "enter":
		if c.confirm {
			return c, c.start()
		}
		return nil, nil
	case "n", "q":
		return nil, nil
	}
	return c, nil
}

// highKey: the user types the target's full name; enter confirms only
// on an exact match.
func (c *Confirm) highKey(msg tea.KeyPressMsg) (ui.Modal, tea.Cmd) {
	switch k := msg.String(); {
	case k == "enter":
		if c.matches() {
			return c, c.start()
		}
	case k == "ctrl+u":
		c.typed = ""
	case k == "backspace":
		if r := []rune(c.typed); len(r) > 0 {
			c.typed = string(r[:len(r)-1])
		}
	case msg.Text != "" && len(c.typed) < maxTyped:
		c.typed += msg.Text
	}
	return c, nil
}

// maxTyped bounds the typed name; ZFS names are at most 255 bytes.
const maxTyped = 256

func (c *Confirm) expected() string {
	if c.plan.TypeToConfirm != "" {
		return c.plan.TypeToConfirm
	}
	if len(c.plan.Targets) == 0 {
		return ""
	}
	return c.plan.Targets[0]
}

func (c *Confirm) matches() bool { return c.expected() != "" && c.typed == c.expected() }

func (c *Confirm) start() tea.Cmd {
	c.stage = running
	p, run := c.plan, c.run
	return func() tea.Msg {
		rep, err := run.Run(context.Background(), p)
		return ranMsg{plan: p, report: rep, err: err}
	}
}

func (c *Confirm) startRebuild() tea.Cmd {
	was, rebuild := c.plan, c.rebuild
	return func() tea.Msg {
		p, err := rebuild(context.Background())
		return rebuiltMsg{was: was, plan: p, err: err}
	}
}

// saveNow saves the plan for later; saving needs no write mode, only a plan that
// is not blocked.
func (c *Confirm) saveNow() {
	switch {
	case c.save == nil:
		c.saved = "This change cannot be saved."
	case len(c.plan.Blocked) > 0:
		c.saved = "Blocked changes are not saved."
	default:
		sha, err := c.save(c.plan)
		if err != nil {
			c.saved = err.Error()
			return
		}
		c.saved = "Saved in Plans as " + sha[:12] + "."
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

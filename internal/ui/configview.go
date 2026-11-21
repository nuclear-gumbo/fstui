// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/nuclear-gumbo/fstui/internal/schedule"

	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Config is view 9: the settings (General), where notifications go and
// what sends them (Notifications), what is sent (Alerts) and what runs on
// a schedule (Schedules), as tabs.
type Config struct {
	general   *Settings
	notify    *Notifications
	alerts    *Alerts
	schedules *Schedules
	tab       int
	width     int
}

const (
	tabGeneral = iota
	tabNotifications
	tabAlerts
	tabSchedules
)

var configTabs = []string{"General", "Notifications", "Alerts", "Schedules"}

// NewConfig returns view 9 with its tabs.
func NewConfig(general *Settings, notify *Notifications, alerts *Alerts, schedules *Schedules) *Config {
	notify.sched = schedules
	return &Config{general: general, notify: notify, alerts: alerts, schedules: schedules}
}

// configTab is a tab that edits a file: a form or review may be open.
type configTab interface {
	Update(msg tea.Msg) tea.Cmd
	Render(d Display, width, height int) string
	Keys() []key.Binding
	Nested() bool
	Typing() bool
}

// editing is the tab shown when it is one that edits; General answers
// otherwise.
func (c *Config) editing() configTab {
	switch c.tab {
	case tabNotifications:
		return c.notify
	case tabAlerts:
		return c.alerts
	}
	return c.general
}

// Selection implements Selector: the selected schedule.
func (c *Config) Selection() (Target, bool) {
	if c.tab != tabSchedules {
		return Target{}, false
	}
	return c.schedules.Selection()
}

// NoChangeReason implements Reasoner: why the selected schedule cannot
// be changed.
func (c *Config) NoChangeReason() string {
	if c.tab != tabSchedules {
		return ""
	}
	return c.schedules.noChangeReason()
}

// Selects implements Selects: only the Schedules tab offers changes, and
// only where schedules can be changed.
func (c *Config) Selects() bool { return c.tab == tabSchedules && c.schedules.cannot == "" }

// Number implements Numbered.
func (c *Config) Number() int { return settingsView }

// Name implements Screen.
func (c *Config) Name() string { return "Config" }

// Claims implements Claimer: on the Notifications and Alerts tabs, a adds
// an entry instead of opening the change menu.
func (c *Config) Claims(msg tea.KeyPressMsg) bool {
	adds := c.tab == tabNotifications || c.tab == tabAlerts
	return adds && !c.editing().Nested() && key.Matches(msg, keys.Get(keys.Actions))
}

// Framed implements Framed: the view draws its panel, with the tabs.
func (c *Config) Framed() bool { return true }

// Init implements Screen.
func (c *Config) Init() tea.Cmd { return nil }

// Nested implements Screen: an edit, form or review in the tab shown.
func (c *Config) Nested() bool { return c.tab != tabSchedules && c.editing().Nested() }

// Typing implements Typing.
func (c *Config) Typing() bool { return c.tab != tabSchedules && c.editing().Typing() }

// Status implements Screen.
func (c *Config) Status() Status { return c.general.Status() }

// Activate implements Activator.
func (c *Config) Activate() tea.Cmd {
	return tea.Batch(c.general.Activate(), c.shown())
}

// Deactivate implements Deactivator.
func (c *Config) Deactivate() tea.Cmd { return c.general.Deactivate() }

// Keys implements Screen.
func (c *Config) Keys() []key.Binding {
	tabs := []key.Binding{keys.As(keys.Right, "next tab"), keys.As(keys.Left, "previous tab")}
	if c.tab == tabSchedules {
		return append(c.schedules.Keys(), tabs...)
	}
	t := c.editing()
	if t.Nested() {
		return t.Keys()
	}
	return append(t.Keys(), tabs...)
}

// Update implements Screen. New settings reach every tab; keys, clicks
// and the wheel the one shown.
func (c *Config) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if !c.Nested() && c.switchTab(msg) {
			return c, c.shown()
		}
		if c.tab == tabNotifications && !c.Nested() && key.Matches(msg, keys.Get(keys.Delivery)) {
			return c, c.setUpSending()
		}
		return c, c.current().Update(msg)
	case ClickMsg:
		if msg.Y > 0 {
			return c, c.current().Update(ClickMsg{X: msg.X - 1 - padX, Y: msg.Y - 1 - padY})
		}
		if t := c.panel().TabAt(c.width, msg.X); t >= 0 && !c.Nested() {
			c.tab = t
			return c, c.shown()
		}
		return c, nil
	case ScrollMsg:
		return c, c.current().Update(msg)
	case savedMsg: // notifications saved config.yaml: every view takes the new settings
		return c, c.general.reload(false)
	}
	return c, tea.Batch(c.general.Update(msg), c.notify.Update(msg), c.alerts.Update(msg), c.schedules.Update(msg))
}

// current is the tab shown.
func (c *Config) current() interface{ Update(tea.Msg) tea.Cmd } {
	if c.tab == tabSchedules {
		return c.schedules
	}
	return c.editing()
}

// shown reads the schedules when their tab comes up, or the
// Notifications tab, which says what sends notifications.
func (c *Config) shown() tea.Cmd {
	if c.tab == tabSchedules || c.tab == tabNotifications {
		return c.schedules.load()
	}
	return nil
}

func (c *Config) switchTab(msg tea.KeyPressMsg) bool {
	switch {
	case key.Matches(msg, keys.Get(keys.Right)):
		c.tab = (c.tab + 1) % len(configTabs)
	case key.Matches(msg, keys.Get(keys.Left)):
		c.tab = (c.tab + len(configTabs) - 1) % len(configTabs)
	default:
		return false
	}
	return true
}

func (c *Config) panel() Panel {
	return Panel{Title: "config", Tabs: configTabs, Active: c.tab, Focused: true}
}

// Render implements Screen.
func (c *Config) Render(d Display, width, height int) string {
	c.width = width
	iw, ih := Inner(width, height)
	cw, ch := max(iw-2*padX, 20), max(ih-padY, 1)
	if tw := c.general.cfg.UI.TextWidth; tw > 0 {
		cw = min(cw, tw)
	}
	var body string
	if c.tab == tabSchedules {
		body = c.schedules.Render(d, cw, ch)
	} else {
		body = c.editing().Render(d, cw, ch)
	}
	return c.panel().Render(d, Pad(body, padX, padY), width, height)
}

// The Config view's margins inside its frame: two columns each side and
// a blank line under the top edge, as Posting pads its body and k9s its
// tables. Text is never wider than ui.text_width, so a full-screen
// terminal does not spread a sentence across it.
const (
	padX = 2
	padY = 1
)

// setUpSending turns to the Schedules tab on fstui watch and opens its
// changes, as a there would: the plan writes and starts the service.
func (c *Config) setUpSending() tea.Cmd {
	if del := deliveryOf(c.schedules, c.notify.cfg.Notifications); !del.known || del.on {
		return nil
	}
	c.tab = tabSchedules
	if !c.schedules.selectJob(schedule.WatchID) {
		return c.shown()
	}
	return func() tea.Msg { return OpenChangesMsg{} }
}

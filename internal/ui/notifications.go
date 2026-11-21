// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/notify"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Notifications is the Config view's Notifications tab: the channels
// fstui check sends to, their secrets (set or not, never shown), and a
// test message to one.
type Notifications struct {
	file    string // the user's config.yaml, for messages
	cfg     config.Config
	secrets SecretsLoader
	info    func() []string // backend lines, such as whether ZED sends events; may be nil
	send    Tester
	cursor  int
	results map[string]string // test results this session, by channel
	top     int               // the row of the first channel, for clicks
	sending string
	message string // the result of the last edit or save
	form    *channelForm
	edit    *channelEdit
	review  *channelReview
	// deleteArmed is the channel d was pressed on once; a second d removes it.
	deleteArmed string
	// sched is the Schedules tab, read for what sends notifications.
	sched *Schedules
}

// Nested reports a form or review open: esc closes it, not the view.
func (n *Notifications) Nested() bool { return n.form != nil || n.review != nil }

// Typing reports a form open: its focused field takes typing, so
// digits, letters and ? are text there, not view keys.
func (n *Notifications) Typing() bool { return n.form != nil }

type testResultMsg struct {
	channel string
	err     error
}

// Tester sends a test message to a channel: notify.Test, or in the demo
// a refusal.
type Tester func(ctx context.Context, n config.Notifications, ch config.Channel, secrets config.Secrets, host string) error

// SecretsLoader reads the secrets files; config.LoadSecrets of them in
// fstui.
type SecretsLoader func() (config.Secrets, []config.Problem)

// NewNotifications returns the tab for cfg, with secrets from
// loadSecrets; info adds the backend's lines; send sends test messages.
func NewNotifications(file string, cfg config.Config, loadSecrets SecretsLoader, info func() []string, send Tester) *Notifications {
	return &Notifications{file: file, cfg: cfg, secrets: loadSecrets, info: info, send: send, results: map[string]string{}}
}

// Keys are the tab's bindings.
func (n *Notifications) Keys() []key.Binding {
	switch {
	case n.review != nil:
		return []key.Binding{keys.As(keys.Write, "write the files"), keys.As(keys.Cancel, "keep them")}
	case n.form != nil:
		return []key.Binding{formNext, formMove, formDone, formDrop}
	}
	add := keys.As(keys.Actions, "add a channel")
	var setUp []key.Binding
	if del := deliveryOf(n.sched, n.cfg.Notifications); del.known && !del.on {
		setUp = []key.Binding{keys.Get(keys.Delivery)}
	}
	if len(n.channels()) == 0 {
		return append([]key.Binding{add}, setUp...)
	}
	out := append([]key.Binding{keys.Get(keys.TestChannel), keys.As(keys.Select, "edit"), add, keys.As(keys.Discard, "remove")}, setUp...)
	if n.edit != nil {
		out = append(out, keys.Get(keys.Save))
	}
	return append(out, keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"))
}

// Update handles keys, the result of a test and new settings.
func (n *Notifications) Update(msg tea.Msg) tea.Cmd {
	chs := n.channels()
	switch msg := msg.(type) {
	case ConfigMsg:
		n.cfg = msg.Config
		n.cursor = min(n.cursor, max(len(n.cfg.Notifications.Channels)-1, 0))
	case testResultMsg:
		n.sending = ""
		n.results[msg.channel] = "sent"
		if msg.err != nil {
			n.results[msg.channel] = "failed: " + msg.err.Error()
		}
	case ScrollMsg:
		n.cursor = max(min(n.cursor+msg.Delta, len(chs)-1), 0)
	case ClickMsg:
		if i := msg.Y - n.top; n.form == nil && n.review == nil && i >= 0 && i < len(chs) {
			n.cursor = i
		}
	case tea.KeyPressMsg:
		return n.key(msg, chs)
	}
	return nil
}

// key handles a key: in the review or form open, else on the list.
func (n *Notifications) key(msg tea.KeyPressMsg, chs []config.Channel) tea.Cmd {
	switch {
	case n.review != nil:
		return n.reviewKey(msg)
	case n.form != nil:
		if done, keep := n.form.key(msg); done {
			f := n.form
			n.form = nil
			if keep {
				n.finishForm(f)
			}
		}
		return nil
	case n.editKey(msg):
		return nil
	}
	if !key.Matches(msg, keys.Get(keys.Discard)) {
		n.deleteArmed = ""
	}
	switch {
	case key.Matches(msg, keys.Get(keys.Down)):
		n.cursor = min(n.cursor+1, max(len(chs)-1, 0))
	case key.Matches(msg, keys.Get(keys.Up)):
		n.cursor = max(n.cursor-1, 0)
	case key.Matches(msg, keys.Get(keys.TestChannel)) && len(chs) > 0 && n.sending == "" && n.edit == nil:
		return n.test(chs[n.cursor])
	case key.Matches(msg, keys.Get(keys.TestChannel)) && n.edit != nil:
		n.message = "Save the changes first (" + keyName(keys.Save) + "): a test uses the saved channel."
	}
	return nil
}

// test sends a test message to ch in the background.
func (n *Notifications) test(ch config.Channel) tea.Cmd {
	n.sending = ch.Name
	load, send, nc := n.secrets, n.send, n.cfg.Notifications
	return func() tea.Msg {
		secrets, problems := load()
		if len(problems) > 0 {
			return testResultMsg{ch.Name, fmt.Errorf("%s", problems[0].String())}
		}
		host, _ := os.Hostname()
		ctx, cancel := context.WithTimeout(context.Background(), nc.SendTimeout+nc.AttemptTimeout)
		defer cancel()
		return testResultMsg{ch.Name, send(ctx, nc, ch, secrets, cmp.Or(nc.Host, host))}
	}
}

// Render draws the tab.
func (n *Notifications) Render(d Display, width, height int) string {
	return styleKeys(d, n.render(d, width, height))
}

func (n *Notifications) render(d Display, width, height int) string {
	switch {
	case n.review != nil:
		return n.renderReview(d, width, height)
	case n.form != nil:
		return n.renderForm(d, width, height)
	}
	muted := d.Palette.Style(theme.Muted)
	nc := n.cfg.Notifications
	nc.Channels = n.channels()
	lines := append(n.setup(d, width), "", lipgloss.NewStyle().Bold(true).Render("Channels"), muted.Render(deliveryNote(width)), "")
	n.top = lipgloss.Height(strings.Join(lines, "\n")) + 1 // the table's first row, under its header
	secrets, problems := n.secrets()
	for _, p := range problems {
		lines = append(lines, d.Palette.Style(theme.Error).Render(ansi.Wrap(p.String(), width, "")), "")
	}
	if len(nc.Channels) == 0 {
		lines = append(lines, ansi.Wrap("No channels yet. "+keyName(keys.Actions)+" adds one: ntfy, a webhook, Gotify, Pushover, Telegram, Discord, Slack, Matrix or mail.", width, ""))
	} else {
		lines = append(lines, n.table(d, secrets)...)
	}
	if n.info != nil {
		lines = append(lines, "")
		for _, l := range n.info() {
			lines = append(lines, muted.Render(ansi.Wrap(l, width, "")))
		}
	}
	if n.edit != nil {
		lines = append(lines, "", ansi.Wrap("Unsaved changes; "+keyName(keys.Save)+" reviews them.", width, ""))
	}
	if len(nc.Channels) > 0 {
		ch := nc.Channels[n.cursor]
		switch r := n.results[ch.Name]; {
		case n.sending == ch.Name:
			lines = append(lines, "", muted.Render("Sending a test message to "+ch.Name+"…"))
		case r != "":
			lines = append(lines, "", ansi.Wrap("Test message to "+ch.Name+": "+r, width, ""))
		}
	}
	if n.message != "" {
		lines = append(lines, "", ansi.Wrap(n.message, width, ""))
	}
	return strings.Join(lines, "\n")
}

// table lists the channels: name, type, where, least severity, secret.
func (n *Notifications) table(d Display, secrets config.Secrets) []string {
	pal := d.Palette
	chs := n.channels()
	nameCol, typeCol, destCol := len("NAME"), len("TYPE"), len("DESTINATION")
	for _, ch := range chs {
		nameCol, typeCol, destCol = max(nameCol, len(ch.Name)), max(typeCol, len(ch.Type)), max(destCol, len(destination(ch)))
	}
	destCol = min(destCol, 40)
	out := []string{pal.Style(theme.Muted).Render("  " + pad(nameCol, "NAME") + "  " + pad(typeCol, "TYPE") + "  " + pad(destCol, "DESTINATION") + "  " + pad(len("warning"), "MIN") + "  SECRET")}
	for i, ch := range chs {
		marker, name := "  ", pad(nameCol, ch.Name)
		if i == n.cursor {
			marker = pal.Style(theme.Accent).Render(">") + " "
		}
		status := pal.Style(theme.Muted).Render("not needed") // whether a secret is set, never the secret
		if n.edit != nil && n.edit.set[ch.Name] != "" {
			status = "set, unsaved"
		} else if _, err := secrets.Secret(ch); err == nil {
			status = "set"
		} else if notify.NeedsSecret(ch) {
			status = pal.Style(theme.Error).Render("missing")
		}
		dest := ansi.Truncate(destination(ch), destCol, "…")
		out = append(out, marker+name+"  "+pad(typeCol, ch.Type)+"  "+pad(destCol, dest)+"  "+pad(len("warning"), ch.MinSeverity)+"  "+status)
	}
	return out
}

// destination is where a channel sends, without its secret.
func destination(ch config.Channel) string {
	host := func(raw string) string {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			return u.Host
		}
		return raw
	}
	switch ch.Type {
	case "ntfy":
		return host(ch.URL) + "/" + ch.Topic
	case "webhook", "gotify":
		return host(ch.URL)
	case "matrix":
		return ch.Room + " on " + host(ch.URL)
	case "telegram":
		return "chat " + ch.ChatID
	case "smtp":
		return strings.Join(ch.To, ", ")
	case "pushover":
		return "Pushover"
	}
	return ch.Type + " webhook"
}

// The keys of a form, the same as the change forms'.
var (
	formNext = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next field"))
	formMove = key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab/shift+tab", "move"))
	formDone = key.NewBinding(key.WithKeys("ctrl+enter"), key.WithHelp("ctrl+enter", "finish"))
	formDrop = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "drop the form"))
)

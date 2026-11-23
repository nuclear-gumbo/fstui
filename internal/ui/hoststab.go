// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"context"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Hosts is the Config view's Hosts tab: the other machines fstui reads
// over SSH (hosts:), which machines fstui check and fstui watch look at
// (notifications.hosts), and a test of a host's connection. It edits
// like the Notifications tab: a form, then the new file as a diff.
type Hosts struct {
	file        string // the user's config.yaml
	cfg         config.Config
	lister      *HostLister // reads a host for t
	cursor      int         // row 0 is this machine, then the hosts
	top         int         // the first row, for clicks
	edit        *hostEdit
	form        *hostForm
	review      *fileReview
	results     map[string]string // t's results this session, by host
	testing     string
	message     string
	deleteArmed string
}

// hostEdit is the tab's unsaved work.
type hostEdit struct {
	hosts   []config.Host
	checked []string // notifications.hosts as it will be; nil leaves it alone
}

// hostForm adds or changes one host.
type hostForm struct {
	fieldForm
	editing string // the host's name before editing; "" for a new one
}

type hostTestMsg struct{ name, text string }

// NewHosts returns the tab for cfg; lister tests a host's connection.
func NewHosts(file string, cfg config.Config, lister *HostLister) *Hosts {
	return &Hosts{file: file, cfg: cfg, lister: lister, results: map[string]string{}}
}

// Nested reports a form or review open: esc closes it, not the view.
func (h *Hosts) Nested() bool { return h.form != nil || h.review != nil }

// Typing reports a form open.
func (h *Hosts) Typing() bool { return h.form != nil }

var hostCheckKey = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "notify or not"))

// Keys are the tab's bindings.
func (h *Hosts) Keys() []key.Binding {
	switch {
	case h.review != nil:
		return []key.Binding{keys.As(keys.Write, "write the file"), keys.As(keys.Cancel, "keep it")}
	case h.form != nil:
		return []key.Binding{formNext, formMove, formDone, formDrop}
	}
	out := []key.Binding{hostCheckKey, keys.As(keys.Actions, "add a host")}
	if h.cursor > 0 {
		out = append(out, keys.As(keys.TestChannel, "test the connection"), editKey, keys.As(keys.Discard, "remove"))
	}
	if h.edit != nil {
		out = append(out, keys.Get(keys.Save))
	}
	return append(out, keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"))
}

// hosts are the hosts as they will be after saving.
func (h *Hosts) hosts() []config.Host {
	if h.edit != nil {
		return h.edit.hosts
	}
	return h.cfg.Hosts
}

// checked are the machines fstui check looks at, as they will be.
func (h *Hosts) checked() []string {
	if h.edit != nil && h.edit.checked != nil {
		return h.edit.checked
	}
	if len(h.cfg.Notifications.Hosts) == 0 {
		return []string{config.LocalHost}
	}
	return h.cfg.Notifications.Hosts
}

// names are the rows: this machine, then each host.
func (h *Hosts) names() []string {
	out := []string{config.LocalHost}
	for _, x := range h.hosts() {
		out = append(out, x.Name)
	}
	return out
}

func (h *Hosts) startEdit() *hostEdit {
	if h.edit == nil {
		h.edit = &hostEdit{hosts: slices.Clone(h.cfg.Hosts)}
	}
	return h.edit
}

// Update handles keys, a test's result and new settings.
func (h *Hosts) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ConfigMsg:
		h.cfg = msg.Config
		h.cursor = min(h.cursor, len(h.names())-1)
	case hostTestMsg:
		h.testing, h.results[msg.name] = "", msg.text
	case ScrollMsg:
		h.cursor = max(min(h.cursor+msg.Delta, len(h.names())-1), 0)
	case ClickMsg:
		if i := msg.Y - h.top; h.form == nil && h.review == nil && i >= 0 && i < len(h.names()) {
			h.cursor = i
		}
	case tea.KeyPressMsg:
		return h.key(msg)
	}
	return nil
}

func (h *Hosts) key(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case h.review != nil:
		cmd, done := h.review.key(msg, &h.message)
		if done {
			if cmd != nil {
				h.saved()
			}
			h.review = nil
		}
		return cmd
	case h.form != nil:
		if done, keep := h.form.key(msg); done {
			f := h.form
			h.form = nil
			if keep {
				h.finishForm(f)
			}
		}
		return nil
	}
	if !key.Matches(msg, keys.Get(keys.Discard)) {
		h.deleteArmed = ""
	}
	return h.listKey(msg)
}

// listKey handles the keys of the list.
func (h *Hosts) listKey(msg tea.KeyPressMsg) tea.Cmd {
	names := h.names()
	switch {
	case key.Matches(msg, keys.Get(keys.Down)):
		h.cursor = min(h.cursor+1, len(names)-1)
	case key.Matches(msg, keys.Get(keys.Up)):
		h.cursor = max(h.cursor-1, 0)
	case key.Matches(msg, hostCheckKey):
		h.toggle(names[h.cursor])
	case key.Matches(msg, keys.Get(keys.Actions)):
		h.openForm(config.Host{})
	case key.Matches(msg, editKey) && h.cursor > 0:
		h.openForm(h.hosts()[h.cursor-1])
	case key.Matches(msg, keys.Get(keys.Discard)) && h.cursor > 0:
		h.remove(names[h.cursor])
	case key.Matches(msg, keys.Get(keys.TestChannel)) && h.cursor > 0 && h.testing == "":
		return h.test(h.hosts()[h.cursor-1])
	case key.Matches(msg, keys.Get(keys.Save)) && h.edit != nil:
		h.review = h.stage()
	}
	return nil
}

func (h *Hosts) openForm(x config.Host) {
	if h.file == "" {
		h.message = "Hosts cannot be changed: fstui could not find your config file (the notice at startup says why)."
		return
	}
	title := "New host"
	if x.Name != "" {
		title = "Host " + x.Name
	}
	h.form, h.message = &hostForm{fieldForm: fieldForm{title: title, values: map[string]string{"name": x.Name, "destination": x.Destination}, fieldsOf: hostFields}, editing: x.Name}, ""
}

func hostFields(map[string]string) []formField {
	return []formField{
		{key: "name", label: "Name", group: "Host", hint: "Lower-case letters, digits, - and _, as --host and the host list show it: pve1"},
		{key: "destination", label: "Destination", group: "Host", hint: "An alias from ~/.ssh/config, or user@host: root@pve1. ssh HOST must log in without a password prompt"},
	}
}

// finishForm applies the form and reviews the file.
func (h *Hosts) finishForm(f *hostForm) {
	x := config.Host{Name: strings.TrimSpace(f.values["name"]), Destination: strings.TrimSpace(f.values["destination"])}
	e := h.startEdit()
	if i := slices.IndexFunc(e.hosts, func(c config.Host) bool { return c.Name == f.editing && f.editing != "" }); i >= 0 {
		e.hosts[i] = x
		h.cursor = i + 1
	} else {
		e.hosts = append(e.hosts, x)
		h.cursor = len(e.hosts)
	}
	if f.editing != "" && f.editing != x.Name && slices.Contains(h.checked(), f.editing) {
		e.checked = replaceName(h.checked(), f.editing, x.Name) // renamed: still checked
	}
	h.review = h.stage()
}

// replaceName is names with from replaced by to.
func replaceName(names []string, from, to string) []string {
	out := slices.Clone(names)
	for i, n := range out {
		if n == from {
			out[i] = to
		}
	}
	return out
}

// remove takes a host out, on the second d.
func (h *Hosts) remove(name string) {
	if h.deleteArmed != name {
		h.deleteArmed, h.message = name, "Press "+keyName(keys.Discard)+" again to remove "+name+"."
		return
	}
	e := h.startEdit()
	e.hosts = slices.DeleteFunc(e.hosts, func(c config.Host) bool { return c.Name == name })
	if slices.Contains(h.checked(), name) {
		e.checked = slices.DeleteFunc(slices.Clone(h.checked()), func(n string) bool { return n == name })
	}
	h.cursor, h.deleteArmed = min(h.cursor, len(e.hosts)), ""
	h.message = name + " removed; " + keyName(keys.Save) + " reviews the change."
}

// toggle checks a machine with fstui check and fstui watch, or not. The
// list keeps the rows' order, and at least one machine stays checked.
func (h *Hosts) toggle(name string) {
	if h.file == "" {
		h.message = "This cannot be changed: fstui could not find your config file (the notice at startup says why)."
		return
	}
	if file := h.cfg.Target(h.file, "notifications.hosts"); file != h.cfg.Target(h.file, "hosts") {
		h.message = "notifications.hosts is set in " + file + ", hosts: elsewhere; change it there."
		return
	}
	on := slices.Contains(h.checked(), name)
	if on && len(h.checked()) == 1 {
		h.message = "fstui watch needs a machine to send notifications about; tick another first."
		return
	}
	want := map[string]bool{name: !on}
	for _, n := range h.checked() {
		if n != name {
			want[n] = true
		}
	}
	var next []string
	for _, n := range h.names() {
		if want[n] {
			next = append(next, n)
			delete(want, n)
		}
	}
	for _, n := range h.checked() { // names the file lists that are not rows stay, for the review to name
		if want[n] {
			next = append(next, n)
		}
	}
	h.startEdit().checked = next
	h.message = ""
}

// saved takes what was just written as the tab's settings: the screen
// is drawn again before the Config view reads the file back, and the
// cursor may be on the host just added.
func (h *Hosts) saved() {
	h.cfg.Hosts = h.edit.hosts
	if h.edit.checked != nil {
		h.cfg.Notifications.Hosts = h.edit.checked
	}
	h.edit = nil
}

// stage writes the new file to a private temporary file for review.
func (h *Hosts) stage() *fileReview {
	file := h.cfg.Target(h.file, "hosts")
	cur, err := config.Read(file)
	if err != nil {
		h.message = err.Error()
		return nil
	}
	next, err := config.EditHosts(cur, h.edit.hosts, h.edit.checked)
	if err != nil {
		h.message = err.Error()
		return nil
	}
	if string(next) == string(cur) {
		h.edit, h.message = nil, "No changes to save."
		return nil
	}
	r, err := newFileReview(h.file, file, cur, next)
	if err != nil {
		h.message = err.Error()
		return nil
	}
	return r
}

// test reads the host as the host list does, in the background.
func (h *Hosts) test(x config.Host) tea.Cmd {
	if h.lister == nil {
		return nil
	}
	h.testing = x.Name
	l, cfg := h.lister, h.cfg
	return func() tea.Msg {
		text, _ := l.Status(context.Background(), cfg, HostEntry{Name: x.Name, Destination: x.Destination})
		return hostTestMsg{name: x.Name, text: text}
	}
}

// Render draws the tab.
func (h *Hosts) Render(d Display, width, height int) string {
	return styleKeys(d, h.render(d, width, height))
}

func (h *Hosts) render(d Display, width, height int) string {
	switch {
	case h.review != nil:
		return h.review.render(d, width, height, nil)
	case h.form != nil:
		return h.form.render(d, width, height, func(formField) (string, bool) { return "", false })
	}
	pal := d.Palette
	muted := pal.Style(theme.Muted)
	intro := "Other machines fstui reads over SSH, read-only; " + keyMark("H") + " switches to one. " +
		keyMark(hostCheckKey.Help().Key) + " chooses the machines fstui watch sends notifications about; " +
		keyName(keys.TestChannel) + " tests a connection."
	lines := []string{ansi.Wrap(intro, width, ""), ""}
	nameCol := len("NAME")
	for _, n := range h.names() {
		nameCol = max(nameCol, len(n))
	}
	lines = append(lines, muted.Render("  NOTIFY  "+pad(nameCol, "NAME")+"  DESTINATION"))
	h.top = lipgloss.Height(strings.Join(lines, "\n"))
	checked := h.checked()
	for i, n := range h.names() {
		marker := "  "
		if i == h.cursor {
			marker = pal.Style(theme.Accent).Render(">") + " "
		}
		box := "[ ]"
		if slices.Contains(checked, n) {
			box = "[x]"
		}
		dest := "this machine"
		if i > 0 {
			dest = termtext.New(h.hosts()[i-1].Destination).Display()
		}
		lines = append(lines, ansi.Truncate(marker+box+"     "+pad(nameCol, n)+"  "+dest, width, "…"))
	}
	if len(h.hosts()) == 0 {
		lines = append(lines, "", ansi.Wrap("No other hosts yet. "+keyName(keys.Actions)+" adds one; docs/remote.md says what a host needs.", width, ""))
	}
	if h.edit != nil {
		lines = append(lines, "", ansi.Wrap("Unsaved changes; "+keyName(keys.Save)+" reviews them.", width, ""))
	}
	if h.cursor > 0 {
		name := h.names()[h.cursor]
		switch r := h.results[name]; {
		case h.testing == name:
			lines = append(lines, "", muted.Render("Connecting to "+name+"…"))
		case r != "":
			lines = append(lines, "", ansi.Wrap(name+": "+termtext.New(r).Display(), width, ""))
		}
	}
	if h.message != "" {
		lines = append(lines, "", ansi.Wrap(h.message, width, ""))
	}
	return strings.Join(lines, "\n")
}

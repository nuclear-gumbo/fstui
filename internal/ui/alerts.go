// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// alertBook is the Alerts tab's settings and its unsaved alerts and own
// templates, written together (config.EditAlerts).
type alertBook struct {
	file  string // the user's config.yaml; "" when it could not be found
	cfg   config.Config
	edit  *alertEdit
	pools func() []string // the pools on this system, for the checklist; may be nil
	// poolList is what pools gave when the form opened: forms are drawn
	// every frame, and the list need not be.
	poolList []string
}

type alertEdit struct {
	alerts    []config.Alert
	templates []config.Template
}

func (b *alertBook) alerts() []config.Alert {
	if b.edit != nil {
		return b.edit.alerts
	}
	return b.cfg.ActiveAlerts()
}

// templates are the user's own, as they will be after saving.
func (b *alertBook) templates() []config.Template {
	if b.edit != nil {
		return b.edit.templates
	}
	return b.cfg.Notifications.Templates
}

// defaults reports that the alerts shown are fstui's defaults: the file
// lists none.
func (b *alertBook) defaults() bool { return b.edit == nil && len(b.cfg.Notifications.Alerts) == 0 }

func (b *alertBook) startEdit() *alertEdit {
	if b.edit == nil {
		b.edit = &alertEdit{alerts: slices.Clone(b.cfg.ActiveAlerts()), templates: slices.Clone(b.cfg.Notifications.Templates)}
	}
	return b.edit
}

// lookup finds a template, the user's (as edited) before the built-in.
func (b *alertBook) lookup(name string) (config.Template, bool) {
	return config.Notifications{Templates: b.templates()}.TemplateNamed(name)
}

func (b *alertBook) mine(name string) bool {
	return slices.ContainsFunc(b.templates(), func(t config.Template) bool { return t.Name == name })
}

// yours is the heading of the user's own alerts.
const yours = "Your own"

// Alerts is the Config view's Alerts tab: every kind of alert fstui can
// send, grouped, each a line to tick, as TrueNAS's alert settings and
// OpenMediaVault's Events page do;
// space turns one on or off, enter changes where and when it is sent,
// a adds your own.
type Alerts struct {
	book        *alertBook
	cursor, top int
	form        *entryForm
	review      *fileReview
	message     string
	deleteArmed string
	lineOf      []int // the screen line of each row, for clicks; set by Render
}

// entryForm is the form for one alert: alert is its index in the list
// (-1 for a new one), template the kind it turns on ("" for your own new
// one), own whether it edits your own alert's condition too.
type entryForm struct {
	fieldForm
	alert    int
	template string
	own      bool
}

// NewAlerts returns the Alerts tab for cfg; pools lists the pools on this
// system for the forms, and may be nil.
func NewAlerts(file string, cfg config.Config, pools func() []string) *Alerts {
	return &Alerts{book: &alertBook{file: file, cfg: cfg, pools: pools}}
}

// Nested reports a form or review open: esc closes it, not the view.
func (a *Alerts) Nested() bool { return a.form != nil || a.review != nil }

// Typing reports a form open: every key is the form's.
func (a *Alerts) Typing() bool { return a.form != nil }

// alertRow is a line of the list: a kind of alert and, when it is on,
// the alert that turns it on (-1 when it is off). A kind turned on twice
// (for different pools) has a row for each.
type alertRow struct {
	t     config.Template
	alert int
	group string
}

// rows lists every kind of alert in its group, the built-in ones in the
// order fstui ships them, then your own.
func (a *Alerts) rows() []alertRow {
	als := a.book.alerts()
	kinds := a.kinds()
	var out []alertRow
	for _, g := range append(slices.Clone(config.Categories), yours) {
		for _, t := range kinds {
			if cmpGroup(t) != g {
				continue
			}
			n := 0
			for i, al := range als {
				if al.Template == t.Name {
					out = append(out, alertRow{t: t, alert: i, group: g})
					n++
				}
			}
			if n == 0 {
				out = append(out, alertRow{t: t, alert: -1, group: g})
			}
		}
	}
	return out
}

// kinds are the built-in templates, your copy in place of one you
// replaced, then your own.
func (a *Alerts) kinds() []config.Template {
	kinds := slices.Clone(config.BuiltinTemplates)
	for i, t := range kinds {
		if mine, ok := a.book.lookup(t.Name); ok && a.book.mine(t.Name) {
			mine.Category, mine.About = t.Category, t.About // your copy of a built-in one stays in its place
			kinds[i] = mine
		}
	}
	for _, t := range a.book.templates() {
		if !slices.ContainsFunc(kinds, func(k config.Template) bool { return k.Name == t.Name }) {
			kinds = append(kinds, t)
		}
	}
	return kinds
}

func cmpGroup(t config.Template) string {
	if t.Category == "" {
		return yours
	}
	return t.Category
}

var (
	toggleKey = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "turn on or off"))
	editKey   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "change"))
)

// Keys are the tab's bindings.
func (a *Alerts) Keys() []key.Binding {
	switch {
	case a.review != nil:
		return []key.Binding{keys.As(keys.Write, "write the file"), keys.As(keys.Cancel, "keep it")}
	case a.form != nil:
		return []key.Binding{formNext, formMove, formDone, formDrop}
	}
	out := []key.Binding{toggleKey, editKey, keys.As(keys.Actions, "add your own")}
	if rs := a.rows(); a.cursor < len(rs) && rs[a.cursor].group == yours {
		out = append(out, keys.As(keys.Discard, "remove"))
	}
	if a.book.edit != nil {
		out = append(out, keys.Get(keys.Save))
	}
	return append(out, keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"))
}

// Update handles keys and new settings.
func (a *Alerts) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ConfigMsg:
		a.book.cfg = msg.Config
		a.cursor = min(a.cursor, max(len(a.rows())-1, 0))
	case ScrollMsg:
		a.cursor = max(min(a.cursor+msg.Delta, len(a.rows())-1), 0)
	case ClickMsg:
		if i := slices.Index(a.lineOf, msg.Y); i >= 0 && a.form == nil && a.review == nil {
			a.cursor = i
		}
	case tea.KeyPressMsg:
		return a.key(msg)
	}
	return nil
}

func (a *Alerts) key(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case a.review != nil:
		cmd, done := a.review.key(msg, &a.message)
		if done {
			if cmd != nil {
				a.book.edit = nil
			}
			a.review = nil
		}
		return cmd
	case a.form != nil:
		if done, keep := a.form.key(msg); done {
			a.finish(keep)
		}
		return nil
	}
	if !key.Matches(msg, keys.Get(keys.Discard)) {
		a.deleteArmed = ""
	}
	rs := a.rows()
	switch {
	case key.Matches(msg, toggleKey) && len(rs) > 0:
		a.toggle(rs[a.cursor])
	case key.Matches(msg, editKey) && len(rs) > 0:
		a.open(rs[a.cursor])
	case key.Matches(msg, keys.Get(keys.Actions)):
		a.openOwn(alertRow{alert: -1, group: yours})
	case key.Matches(msg, keys.Get(keys.Discard)) && len(rs) > 0:
		a.remove(rs[a.cursor])
	case key.Matches(msg, keys.Get(keys.Save)) && a.book.edit != nil:
		a.review = a.stage()
	case key.Matches(msg, keys.Get(keys.Down)):
		a.cursor = min(a.cursor+1, max(len(rs)-1, 0))
	case key.Matches(msg, keys.Get(keys.Up)):
		a.cursor = max(a.cursor-1, 0)
	case key.Matches(msg, keys.Get(keys.First)):
		a.cursor = 0
	case key.Matches(msg, keys.Get(keys.Last)):
		a.cursor = max(len(rs)-1, 0)
	}
	return nil
}

// canEdit says why alerts cannot be changed, or "".
func (a *Alerts) canEdit() bool {
	if a.book.file == "" {
		a.message = "Alerts cannot be changed: fstui could not find your config file (the notice at startup says why)."
		return false
	}
	return true
}

// toggle turns a kind of alert on (for every pool and channel) or the
// selected alert off.
func (a *Alerts) toggle(r alertRow) {
	if !a.canEdit() {
		return
	}
	e := a.book.startEdit()
	if r.alert >= 0 {
		e.alerts = slices.Delete(e.alerts, r.alert, r.alert+1)
		a.message = r.t.Desc + ": off"
	} else {
		e.alerts = append(e.alerts, config.Alert{Template: r.t.Name})
		a.message = r.t.Desc + ": on, for every pool and channel; enter changes that"
	}
	a.message += ". " + keyName(keys.Save) + " reviews the change."
	a.follow(r.t.Name)
}

// follow keeps the cursor on the kind named after the list changed.
func (a *Alerts) follow(name string) {
	if i := slices.IndexFunc(a.rows(), func(r alertRow) bool { return r.t.Name == name }); i >= 0 {
		a.cursor = i
	}
}

// remove deletes your own alert, and everything that turns it on, after
// a second press; a built-in one is turned off with space instead.
func (a *Alerts) remove(r alertRow) {
	if r.group != yours {
		a.message = "Built-in alerts are not removed; space turns this one off."
		return
	}
	if !a.canEdit() {
		return
	}
	if a.deleteArmed != r.t.Name {
		a.deleteArmed, a.message = r.t.Name, "Press "+keyName(keys.Discard)+" again to remove "+r.t.Desc+"."
		return
	}
	e := a.book.startEdit()
	e.templates = slices.DeleteFunc(e.templates, func(t config.Template) bool { return t.Name == r.t.Name })
	e.alerts = slices.DeleteFunc(e.alerts, func(al config.Alert) bool { return al.Template == r.t.Name })
	a.message = r.t.Desc + " removed; " + keyName(keys.Save) + " reviews the change."
	a.cursor, a.deleteArmed = max(min(a.cursor, len(a.rows())-1), 0), ""
}

// stage writes the new file to a private temporary file and lists what
// changes.
func (a *Alerts) stage() *fileReview {
	e := a.book.edit
	file := a.book.cfg.Target(a.book.file, "notifications.alerts")
	cur, err := config.Read(file)
	if err != nil {
		a.message = err.Error()
		return nil
	}
	next, err := config.EditAlerts(cur, e.alerts, e.templates)
	if err != nil {
		a.message = err.Error()
		return nil
	}
	if string(next) == string(cur) {
		a.book.edit, a.message = nil, "No changes to save."
		return nil
	}
	r, err := newFileReview(a.book.file, file, cur, next)
	if err != nil {
		a.message = err.Error()
		return nil
	}
	return r
}

// Render draws the tab.
func (a *Alerts) Render(d Display, width, height int) string {
	return styleKeys(d, a.render(d, width, height))
}

func (a *Alerts) render(d Display, width, height int) string {
	switch {
	case a.review != nil:
		return a.review.render(d, width, height, nil)
	case a.form != nil:
		return a.form.render(d, width, height, nil)
	}
	muted := d.Palette.Style(theme.Muted)
	intro := "Choose what fstui tells you about. Messages go to the channels in the Notifications tab. " + keyMark(toggleKey.Help().Key) + " turns an alert on or off; " + keyMark(editKey.Help().Key) + " sets its pools, channels and thresholds."
	if a.book.defaults() {
		intro += " These are the defaults: your file lists no alerts yet."
	}
	head := []string{muted.Render(ansi.Wrap(intro, width, "")), ""}
	rs := a.rows()
	if len(rs) == 0 {
		return strings.Join(head, "\n")
	}
	a.cursor = min(a.cursor, len(rs)-1)
	detail := Box(d, rs[a.cursor].t.Desc, a.detail(d, rs[a.cursor], width-3), width)
	var foot []string
	if a.book.edit != nil {
		foot = append(foot, "", "Unsaved changes; "+keyName(keys.Save)+" reviews them.")
	}
	if a.message != "" {
		foot = append(foot, "", ansi.Wrap(a.message, width, ""))
	}
	list, lineOf := a.list(d, rs, width)
	room := max(height-lipgloss.Height(strings.Join(head, "\n"))-len(detail)-len(foot)-1, 3)
	at := lineOf[a.cursor]
	a.top = min(max(a.top, at-room+1), at)
	if a.cursor == 0 {
		a.top = 0
	}
	a.top = max(min(a.top, len(list)-room), 0)
	shown := list[a.top:min(a.top+room, len(list))]
	base := lipgloss.Height(strings.Join(head, "\n"))
	a.lineOf = make([]int, len(lineOf))
	for i, l := range lineOf {
		a.lineOf[i] = base + l - a.top
	}
	out := append(head, shown...)
	out = append(append(out, ""), detail...)
	return strings.Join(append(out, foot...), "\n")
}

// list draws the groups and their rows, and where each row is.
func (a *Alerts) list(d Display, rs []alertRow, width int) ([]string, []int) {
	pal := d.Palette
	descCol, whenCol := 0, 0
	for _, r := range rs {
		descCol = max(descCol, lipgloss.Width(r.t.Desc))
		whenCol = max(whenCol, lipgloss.Width(a.when(r)))
	}
	descCol, whenCol = min(descCol, width/2), min(whenCol, width/4)
	var out []string
	lineOf := make([]int, len(rs))
	group := ""
	for i, r := range rs {
		if r.group != group {
			if group != "" {
				out = append(out, "")
			}
			group = r.group
			out = append(out, lipgloss.NewStyle().Bold(true).Render(group))
		}
		box, scope := "[ ]", ""
		if r.alert >= 0 {
			box, scope = "[x]", a.scope(r)
		}
		line := box + " " + pad(descCol, ansi.Truncate(r.t.Desc, descCol, "…")) + "   " + pad(whenCol, ansi.Truncate(a.when(r), whenCol, "…")) + "   " + scope
		line = ansi.Truncate(strings.TrimRight(line, " "), width-2, "…")
		switch {
		case i == a.cursor:
			line = pal.Style(theme.Accent).Render(">") + " " + lipgloss.NewStyle().Bold(true).Render(line)
		case r.alert < 0:
			line = "  " + pal.Style(theme.Muted).Render(line)
		default:
			line = "  " + line
		}
		lineOf[i] = len(out)
		out = append(out, line)
	}
	if !slices.ContainsFunc(rs, func(r alertRow) bool { return r.group == yours }) {
		out = append(out, "", lipgloss.NewStyle().Bold(true).Render(yours),
			pal.Style(theme.Muted).Render("  None yet; "+keyName(keys.Actions)+" adds one: any measure of a pool, your threshold, your message."))
	}
	return out, lineOf
}

// effective is a row's template as its alert sets it.
func (a *Alerts) effective(r alertRow) config.Template {
	if r.alert >= 0 {
		return a.book.alerts()[r.alert].Effective(r.t)
	}
	return r.t
}

func (a *Alerts) when(r alertRow) string { return a.effective(r).When() }

// scope is where an alert applies and is sent: "all pools", "tank,
// vault", with "→ phone" when it goes to some channels only. An alert
// about the system names no pools.
func (a *Alerts) scope(r alertRow) string {
	al := a.book.alerts()[r.alert]
	out := "all pools"
	if len(al.Pools) > 0 {
		out = strings.Join(al.Pools, ", ")
	}
	if r.t.Category == "System" {
		out = "all channels"
		if len(al.Channels) > 0 {
			out = "→ " + strings.Join(al.Channels, ", ")
		}
		return out
	}
	if len(al.Channels) > 0 {
		out += " → " + strings.Join(al.Channels, ", ")
	}
	return out
}

// detail explains the selected alert: what it watches, the message it
// sends, filled in for an example pool, and how often.
func (a *Alerts) detail(d Display, r alertRow, width int) []string {
	muted := d.Palette.Style(theme.Muted)
	t := a.effective(r)
	about := t.About
	if about == "" { // your own: say what it watches
		m, _ := config.MetricNamed(t.Metric)
		about = "Watches " + m.Desc + "."
	}
	out := []string{ansi.Wrap(about, width, "")}
	if r.alert < 0 {
		out = append(out, ansi.Wrap("Off: nothing is sent. "+keyMark(toggleKey.Help().Key)+" turns it on for every pool and channel.", width, ""))
	}
	out = append(out, "", ansi.Wrap("Sends, for example: "+t.Example(t.Text), width, ""))
	if t.ResolvedText != "" {
		out = append(out, ansi.Wrap("And when it clears: "+t.Example(t.ResolvedText), width, ""))
	}
	if r.alert >= 0 {
		al := a.book.alerts()[r.alert]
		out = append(out, muted.Render(ansi.Wrap(repeatWords(al.Repeat, a.book.cfg.Notifications.Repeat)+"; severity "+t.Severity+".", width, "")))
	}
	return out
}

// repeatWords says how often an open problem is sent again.
func repeatWords(r *time.Duration, def time.Duration) string {
	d := def
	if r != nil {
		d = *r
	}
	if d == 0 {
		return "Sent once while it lasts"
	}
	return "Sent again " + repeatShow(config.FormatDuration(d)) + " while it lasts"
}

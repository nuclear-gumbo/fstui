// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// Offer is one change offered for a selection.
type Offer struct {
	Key      string    // one letter, unique in the menu
	Name     string    // "Create snapshot"
	Op       mutate.Op // what it asks for comes from its tier range
	Disabled string    // why it cannot be offered, or empty
	Fields   []Field
	Build    Build
	// Need lists the delegated permissions the change needs (zfs-allow(8)),
	// from the form's values where they matter (setting a property needs
	// that property's permission). Nil needs none.
	Need func(values map[string]string) []string
	// Check, if set, says why the change cannot be made now ("no scrub
	// is running"), or "". The menu asks when it opens, so a change
	// that would be refused shows as unavailable before it is chosen.
	Check func(ctx context.Context) string
	// BuildSecret replaces Build for a change that needs a passphrase,
	// typed into the form's Secret fields.
	BuildSecret BuildSecret
}

// needs returns a Need for a fixed list of permissions.
func needs(perms ...string) func(map[string]string) []string {
	return func(map[string]string) []string { return perms }
}

// Permissions reads what the user may do on the selection; nil when
// fstui runs as root and may do everything.
type Permissions func(ctx context.Context) (map[string]bool, error)

type permsMsg struct {
	menu    *Menu
	perms   map[string]bool // nil when not limited
	err     error
	reasons map[int]string // offer index -> why it cannot be made now
}

// missing returns the permissions in need that perms lacks.
func missing(need []string, perms map[string]bool) []string {
	var out []string
	for _, p := range need {
		if !perms[p] {
			out = append(out, p)
		}
	}
	return out
}

// Menu lists the changes offered for a selection. Choosing one opens
// its form, or straight away its preview when it has no parameters.
type Menu struct {
	title    string
	offers   []Offer
	cursor   int
	confirm  func(*mutate.Plan, Rebuild) ui.Modal
	form     *Form           // a form without fields, building
	perms    map[string]bool // delegated permissions; nil when not limited
	checking bool            // permissions are being read
	auto     string          // offer to choose once permissions are known
}

// autoChoose makes the menu choose the offer with key k as soon as it
// can; if that offer is disabled, the menu stays open and says why.
func (m *Menu) autoChoose(k string, cmd tea.Cmd) (ui.Modal, tea.Cmd) {
	if m.checking {
		m.auto = k
		return m, cmd
	}
	return m.chooseKey(k)
}

func (m *Menu) chooseKey(k string) (ui.Modal, tea.Cmd) {
	for i, o := range m.offers {
		if o.Key == k {
			return m.choose(i)
		}
	}
	return m, nil
}

// NewMenu opens the menu for offers; title names the selection. With
// perms set, it returns the command that reads them; changes stay
// unavailable until they are known.
func NewMenu(title string, offers []Offer, confirm func(*mutate.Plan, Rebuild) ui.Modal, perms Permissions) (*Menu, tea.Cmd) {
	m := &Menu{title: title, offers: offers, confirm: confirm}
	checks := map[int]func(context.Context) string{}
	for i, o := range offers {
		if o.Check != nil {
			checks[i] = o.Check
		}
	}
	if perms == nil && len(checks) == 0 {
		return m, nil
	}
	m.checking = true
	return m, func() tea.Msg {
		ctx := context.Background()
		msg := permsMsg{menu: m, reasons: map[int]string{}}
		if perms != nil {
			msg.perms, msg.err = perms(ctx)
		}
		for i, check := range checks {
			msg.reasons[i] = check(ctx)
		}
		return msg
	}
}

// limit disables the offers whose fixed needs are not delegated.
func (m *Menu) limit(msg permsMsg) {
	m.checking = false
	for i, why := range msg.reasons {
		if why != "" && m.offers[i].Disabled == "" {
			m.offers[i].Disabled = why
		}
	}
	if msg.err != nil {
		for i := range m.offers {
			m.offers[i].Disabled = "cannot read the delegated permissions: " + msg.err.Error()
		}
		return
	}
	m.perms = msg.perms
	if m.perms == nil {
		return // fstui runs as root: only the checks above apply
	}
	for i, o := range m.offers {
		if o.Disabled != "" || o.Need == nil || len(o.Fields) > 0 && needsValues(o) {
			continue // checked by the form once the values are known
		}
		if miss := missing(o.Need(nil), m.perms); len(miss) > 0 {
			m.offers[i].Disabled = "needs root or delegated " + strings.Join(miss, ", ") + " on " + termtext.New(m.title).Display()
		}
	}
}

// needsValues reports whether an offer's needs depend on its form.
func needsValues(o Offer) bool { return o.Need(nil) == nil }

// Busy implements ui.Modal.
func (m *Menu) Busy() bool { return false }

// Keys implements ui.Modal.
func (m *Menu) Keys() []key.Binding {
	return []key.Binding{keys.As(keys.Select, "choose"), keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"), keyEscCancel}
}

// Update implements ui.Modal.
func (m *Menu) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	if m.form != nil {
		return m.form.Update(msg)
	}
	if pm, ok := msg.(permsMsg); ok && pm.menu == m {
		m.limit(pm)
		if m.auto != "" {
			return m.chooseKey(m.auto)
		}
		return m, nil
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case k.String() == "esc" || key.Matches(k, keys.Get(keys.Back)):
		return nil, nil
	case key.Matches(k, keys.Get(keys.Down)):
		m.cursor = min(m.cursor+1, len(m.offers)-1)
	case key.Matches(k, keys.Get(keys.Up)):
		m.cursor = max(m.cursor-1, 0)
	case key.Matches(k, keys.Get(keys.Select)):
		if len(m.offers) == 0 {
			return m, nil
		}
		return m.choose(m.order()[m.cursor])
	default:
		for i, o := range m.offers {
			if o.Key == k.String() {
				return m.choose(i)
			}
		}
	}
	return m, nil
}

// order lists the offers as shown: those possible now first, then
// those that are not, each group in the order offered. The cursor is a
// position in this order.
func (m *Menu) order() []int {
	var on, off []int
	for i, o := range m.offers {
		if o.Disabled == "" {
			on = append(on, i)
		} else {
			off = append(off, i)
		}
	}
	return append(on, off...)
}

func (m *Menu) choose(i int) (ui.Modal, tea.Cmd) {
	m.cursor = slices.Index(m.order(), i)
	o := m.offers[i]
	if o.Disabled != "" || m.checking {
		return m, nil
	}
	f := NewForm(o.Name, o.Fields, o.Build, m.confirm)
	if o.BuildSecret != nil {
		f.withSecret(o.BuildSecret)
	}
	if m.perms != nil && o.Need != nil {
		f.need, f.perms, f.on = o.Need, m.perms, m.title
	}
	if len(o.Fields) > 0 {
		return f, nil
	}
	m.form = f
	f.busy = true
	return m, func() tea.Msg {
		p, err := o.Build(context.Background(), nil)
		return builtMsg{form: f, plan: p, err: err}
	}
}

// Render implements ui.Modal.
func (m *Menu) Render(d ui.Display, width, height int) string {
	if m.form != nil {
		return m.form.Render(d, width, height)
	}
	w := min(width, maxModalWidth)
	iw, _ := ui.Inner(w, height)
	muted := d.Palette.Style(theme.Muted)
	lines := m.offerLines(d, iw)
	order := m.order()
	if len(m.offers) == 0 {
		lines = append(lines, muted.Render("No changes are offered for this selection yet."))
	}
	if m.checking {
		lines = append(lines, "", muted.Render("Checking what can be done now…"))
	}
	lines = append(lines, "")
	if m.cursor < len(order) {
		o := m.offers[order[m.cursor]]
		if o.Disabled == "" {
			lines = append(lines, muted.Render(fit(asks(o.Op), iw)))
		}
	}
	lines = append(lines, muted.Render(fit("Change applies following confirmation.", iw)))
	title := "changes: " + termtext.New(m.title).Display()
	return ui.Panel{Title: fit(title, iw-4), Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

// offerLines lists the changes possible now, then under a heading those
// that are not, each with its reason.
func (m *Menu) offerLines(d ui.Display, iw int) []string {
	accent, muted := d.Palette.Style(theme.Accent).Bold(true), d.Palette.Style(theme.Muted)
	lines := []string{}
	nameCol := 0 // of the unavailable changes, to line up the reasons
	for _, o := range m.offers {
		if o.Disabled != "" {
			nameCol = max(nameCol, len(o.Name))
		}
	}
	order := m.order()
	for pos, i := range order {
		o := m.offers[i]
		if o.Disabled != "" && (pos == 0 || m.offers[order[pos-1]].Disabled == "") {
			if pos > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, muted.Render("  Unavailable"))
		}
		line := accent.Render(o.Key) + "  " + o.Name
		if o.Disabled != "" {
			line = muted.Render(o.Key + "  " + fmt.Sprintf("%-*s", nameCol, o.Name) + "  " + safe(o.Disabled))
		}
		if pos == m.cursor {
			line = "> " + line
		} else {
			line = "  " + line
		}
		lines = append(lines, fit(line, iw))
	}
	return lines
}

// asks says how a change is confirmed: y; y then enter; typing the
// name; or, where it depends on the choices in the form (recursive,
// several items), the least and the most it asks.
func asks(op mutate.Op) string {
	least, most := mutate.Range(op)
	word := map[mutate.Tier]string{mutate.Low: "y", mutate.Medium: "y, then enter", mutate.High: "typing the name"}
	how := map[mutate.Tier]string{mutate.Low: "with y", mutate.Medium: "with y, then enter", mutate.High: "by typing the name"}
	if least == most {
		return "Confirm " + how[least] + "."
	}
	return "Confirm " + how[least] + "; some choices ask for " + word[most] + "."
}

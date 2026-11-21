// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
)

// formField is one row of a form; hint is shown while it is selected,
// and placeholder, muted, while it is empty: what an empty field means.
// A field with choices steps through them; one with two choices "false"
// and "true" is a switch, drawn [x]; secret is typed hidden.
type formField struct {
	key, label, hint, placeholder string
	choices                       []string
	secret                        bool
	// group is the heading the field sits under; a new group starts with
	// a blank line and its heading, so a long form reads as short ones
	// (NN/g, form white space).
	group string
	// multi makes the choices a checklist: several can be picked, kept in
	// the value separated by commas; none picked is the placeholder.
	multi bool
	// show writes a choice for the reader ("every day" for 24h); nil shows
	// it as it is.
	show func(string) string
}

func (fl formField) shown(v string) string {
	if fl.show != nil {
		return fl.show(v)
	}
	return v
}

func (fl formField) isSwitch() bool { return slices.Equal(fl.choices, []string{"false", "true"}) }

// fieldForm is the behaviour every form in the Config view shares, the
// same as the change forms': the focused field takes typing at once,
// enter goes to the next field and finishes on the last, tab and
// shift+tab move, ctrl+enter finishes from anywhere, esc drops the form.
// A choice steps with space and the arrows, or jumps to the choice a
// typed letter starts.
type fieldForm struct {
	title    string
	values   map[string]string
	secret   string // what was typed into the secret field, if any
	cursor   int
	sub      int                                        // the item under the cursor in a checklist field
	top      int                                        // the first line shown when the form is taller than the screen
	problem  string                                     // why the form could not be finished, shown until the next key
	fieldsOf func(values map[string]string) []formField // may depend on a choice, such as a channel's type
}

func (f *fieldForm) fields() []formField { return f.fieldsOf(f.values) }

// key handles a key; done reports that the user left the form, and keep
// whether with the edits.
func (f *fieldForm) key(msg tea.KeyPressMsg) (done, keep bool) {
	fs := f.fields()
	f.cursor = min(f.cursor, len(fs)-1)
	fl := fs[f.cursor]
	f.problem = ""
	before := f.cursor
	defer func() {
		if f.cursor != before {
			f.sub = 0
		}
	}()
	switch k := msg.String(); {
	case k == "esc":
		return true, false
	case k == "enter" && f.cursor < len(fs)-1:
		f.cursor++
	case k == "enter" || k == "ctrl+enter" || k == "alt+enter":
		return true, true
	case fl.multi && (k == "down" || k == "up" || k == "j" || k == "k") && f.inList(fl, k):
	case k == "tab" || k == "down":
		f.cursor = (f.cursor + 1) % len(fs)
	case k == "shift+tab" || k == "up":
		f.cursor = (f.cursor + len(fs) - 1) % len(fs)
	case fl.multi:
		f.check(fl, k)
	case fl.choices != nil:
		f.step(fl, k, msg.Text)
	case k == "backspace":
		f.erase(fl)
	case msg.Text != "" && !strings.ContainsFunc(msg.Text, unicode.IsControl):
		f.typeText(fl, msg.Text)
	}
	return false, false
}

// step moves a choice on with space or right, back with left; a letter
// picks the next choice starting with it. Letters never step, so text
// typed into the wrong field cannot change a choice unseen.
func (f *fieldForm) step(fl formField, k, text string) {
	i, n := slices.Index(fl.choices, f.values[fl.key]), len(fl.choices)
	switch {
	case k == "space" || k == "right":
		f.values[fl.key] = fl.choices[(i+1)%n]
	case k == "left":
		f.values[fl.key] = fl.choices[(i+n-1)%n]
	case len(text) == 1:
		for d := 1; d <= n; d++ {
			if c := fl.choices[(i+d)%n]; strings.HasPrefix(strings.ToLower(fl.shown(c)), strings.ToLower(text)) {
				f.values[fl.key] = c
				return
			}
		}
	}
}

// inList moves within a checklist with up and down, and reports whether
// it did: past its first or last item they leave the field.
func (f *fieldForm) inList(fl formField, k string) bool {
	switch {
	case (k == "down" || k == "j") && f.sub < len(fl.choices)-1:
		f.sub++
	case (k == "up" || k == "k") && f.sub > 0:
		f.sub--
	default:
		return false
	}
	return true
}

// check moves along a checklist with the arrows and picks or drops the
// item under the cursor with space.
func (f *fieldForm) check(fl formField, k string) {
	n := len(fl.choices)
	if n == 0 {
		return
	}
	switch k {
	case "right":
		f.sub = (f.sub + 1) % n
	case "left":
		f.sub = (f.sub + n - 1) % n
	case "space", "x":
		picked := splitNames(f.values[fl.key])
		item := fl.choices[min(f.sub, n-1)]
		if i := slices.Index(picked, item); i >= 0 {
			picked = slices.Delete(picked, i, i+1)
		} else {
			picked = append(picked, item)
		}
		// keep the checklist's order
		var out []string
		for _, c := range fl.choices {
			if slices.Contains(picked, c) {
				out = append(out, c)
			}
		}
		f.values[fl.key] = strings.Join(out, ", ")
	}
}

func (f *fieldForm) erase(fl formField) {
	if fl.secret {
		f.secret = dropLast(f.secret)
		return
	}
	f.values[fl.key] = dropLast(f.values[fl.key])
}

func dropLast(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

// maxField bounds a typed value; the longest are URLs, tokens and
// message templates.
const maxField = 512

func (f *fieldForm) typeText(fl formField, t string) {
	dst := &f.values
	if fl.secret {
		if len(f.secret)+len(t) <= maxField {
			f.secret += t
		}
		return
	}
	if len((*dst)[fl.key])+len(t) <= maxField {
		(*dst)[fl.key] += t
	}
}

// formHelp is the line under every form.
var formHelp = keyMark("enter") + " goes to the next field and, on the last, finishes the form; " + keyMark("tab") + " and " + keyMark("shift+tab") + " move; " + keyMark("space") + ", the arrows or its first letter change a choice; in a list, the arrows move and " + keyMark("space") + " ticks; " + keyMark("esc") + " drops the form."

// render draws the form: a row per field, the focused one's label in the
// accent colour with the typing cursor, its hint, and the keys. shown,
// if set, gives a field's value in place of the typed one (a secret's
// state).
func (f *fieldForm) render(d Display, width, height int, shown func(formField) (string, bool)) string {
	pal := d.Palette
	accent := pal.Style(theme.Accent).Bold(true)
	fs := f.fields()
	labelCol := 0
	for _, fl := range fs {
		labelCol = max(labelCol, len(fl.label))
	}
	lines := []string{accent.Render(f.title)}
	focusAt := 0 // the line of the selected field, or of its checklist item
	group := "\x00"
	for i, fl := range fs {
		if fl.group != group {
			group = fl.group
			lines = append(lines, "")
			if group != "" {
				lines = append(lines, lipgloss.NewStyle().Bold(true).Render(group))
			}
		}
		focused := i == f.cursor
		line, extra := f.row(d, fl, focused, labelCol, shown)
		if focused {
			focusAt = len(lines) + min(f.sub+1, len(extra))
		}
		lines = append(lines, ansi.Truncate(line, width, "…"))
		lines = append(lines, extra...)
	}
	var foot []string
	if hint := fs[f.cursor].hint; hint != "" {
		foot = append(foot, "", ansi.Wrap(hint+".", width, ""))
	}
	if f.problem != "" {
		foot = append(foot, "", pal.Style(theme.Error).Render(ansi.Wrap("Cannot finish: "+f.problem, width, "")))
	}
	foot = append(foot, "", pal.Style(theme.Muted).Render(ansi.Wrap(formHelp, width, "")))
	footH := lipgloss.Height(strings.Join(foot, "\n"))
	if room := height - footH; room > 2 && len(lines) > room {
		// scroll the fields, the title kept, so the selected one shows
		f.top = min(max(f.top, focusAt-room+2), focusAt-1, len(lines)-room+1)
		f.top = max(f.top, 1)
		lines = append(lines[:1], lines[f.top:min(f.top+room-1, len(lines))]...)
	}
	return strings.Join(append(lines, foot...), "\n")
}

// checklist is a checklist field's row: the picked items, or, while it
// is selected, how many are picked (the items are listed under it).
func (f *fieldForm) checklist(d Display, fl formField, focused bool) string {
	picked := splitNames(f.values[fl.key])
	if !focused {
		return strings.Join(picked, ", ")
	}
	if len(picked) == 0 {
		return d.Palette.Style(theme.Muted).Render("none ticked: " + fl.placeholder)
	}
	return fmt.Sprintf("%d ticked", len(picked))
}

// checkItems are a checklist's items, one a line, ticked or not, the one
// under the cursor highlighted (huh's multiple choice).
func (f *fieldForm) checkItems(d Display, fl formField, indent int) []string {
	picked := splitNames(f.values[fl.key])
	accent := d.Palette.Style(theme.Accent).Bold(true)
	out := make([]string, len(fl.choices))
	for i, c := range fl.choices {
		box := "[ ] "
		if slices.Contains(picked, c) {
			box = "[x] "
		}
		line := box + c
		if i == f.sub {
			line = accent.Render("> " + line)
		} else {
			line = "  " + line
		}
		out[i] = strings.Repeat(" ", indent) + line
	}
	return out
}

// row is one field: its label and value, and a checklist's items under it
// while it is selected.
func (f *fieldForm) row(d Display, fl formField, focused bool, labelCol int, shown func(formField) (string, bool)) (string, []string) {
	pal := d.Palette
	accent := pal.Style(theme.Accent).Bold(true)
	var extra []string // a checklist's items, under its row
	v := termtext.New(fl.shown(f.values[fl.key])).Display()
	switch alt, ok := "", false; {
	case fl.multi:
		v = f.checklist(d, fl, focused)
		if focused {
			extra = f.checkItems(d, fl, labelCol+4)
		}
	case fl.secret && f.secret != "":
		v = strings.Repeat("*", len([]rune(f.secret)))
	case shown != nil && func() bool { alt, ok = shown(fl); return ok }():
		v = pal.Style(theme.Muted).Render(alt)
	case fl.isSwitch():
		v = map[bool]string{true: "[x]", false: "[ ]"}[f.values[fl.key] == "true"]
	case fl.choices != nil && focused:
		v = "‹ " + v + " ›"
	}
	label := pad(labelCol, fl.label)
	if focused {
		label = accent.Render(label)
		if fl.choices == nil {
			v += Cursor(d)
		}
	}
	if (v == "" || v == Cursor(d)) && (!fl.multi || !focused) {
		v += pal.Style(theme.Muted).Render(fl.placeholder)
	}
	return "  " + label + "  " + v, extra
}

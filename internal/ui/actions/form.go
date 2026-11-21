// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// Field is one parameter of a change: typed text, or a switch.
type Field struct {
	Name    string // key in the values passed to Build
	Label   string
	Default string // "true" or "false" for a switch
	Switch  bool
	// Secret is a passphrase: kept in a buffer the form clears, shown
	// as stars, given to BuildSecret and never put in the values.
	Secret bool
	// Repeat names a secret field this one must match.
	Repeat string
	// Optional fields may stay empty; the others are marked with * and
	// must be filled before the change is shown.
	Optional bool
}

// BuildSecret makes the plan from the form's values and a passphrase.
// The plan gets its own copy; the caller clears secret afterwards.
type BuildSecret func(ctx context.Context, values map[string]string, secret []byte) (*mutate.Plan, error)

// maxSecret bounds a typed passphrase; its buffer is allocated once at
// this size, so typing never leaves copies behind in old buffers.
const maxSecret = 512

// Build makes the plan from the form's values. It may read the system
// (dry runs, listings), so it runs as a command, not in Update.
type Build func(ctx context.Context, values map[string]string) (*mutate.Plan, error)

// Form asks for a change's parameters, then builds its plan and hands
// over to the confirmation.
type Form struct {
	title   string
	fields  []Field
	values  []string
	focus   int
	build   Build
	confirm func(*mutate.Plan, Rebuild) ui.Modal
	err     string
	busy    bool
	// need, perms and on check delegated permissions before planning;
	// need is nil when fstui runs as root.
	need  func(map[string]string) []string
	perms map[string]bool
	on    string
	// secrets holds the typed passphrases by field index.
	secrets     map[int][]byte
	buildSecret BuildSecret
}

// withSecret makes the form build with a passphrase.
func (f *Form) withSecret(b BuildSecret) *Form {
	f.buildSecret = b
	f.secrets = map[int][]byte{}
	for i, fl := range f.fields {
		if fl.Secret {
			f.secrets[i] = make([]byte, 0, maxSecret)
		}
	}
	return f
}

// required reports whether a field must be filled: typed text or a
// passphrase not marked optional.
func required(fl Field) bool { return !fl.Switch && !fl.Optional }

// firstEmpty is the first required field still empty, or -1.
func (f *Form) firstEmpty() int {
	for i, fl := range f.fields {
		if required(fl) && (fl.Secret && len(f.secrets[i]) == 0 || !fl.Secret && f.values[i] == "") {
			return i
		}
	}
	return -1
}

// last reports whether the focus is on the last field.
func (f *Form) last() bool { return f.focus == len(f.fields)-1 }

// wipe clears every typed passphrase.
func (f *Form) wipe() {
	for i, s := range f.secrets {
		clear(s[:cap(s)])
		f.secrets[i] = s[:0]
	}
}

// secret checks repeats and returns a copy of the passphrase, or why not.
func (f *Form) secret() ([]byte, string) {
	var first []byte
	for i, fl := range f.fields {
		if !fl.Secret {
			continue
		}
		if fl.Repeat == "" && first == nil {
			first = f.secrets[i]
		}
		for j, other := range f.fields {
			if other.Name == fl.Repeat && !bytes.Equal(f.secrets[i], f.secrets[j]) {
				return nil, "The two passphrases are not the same."
			}
		}
	}
	return bytes.Clone(first), ""
}

// NewForm opens a form; confirm opens the preview of the plan built.
func NewForm(title string, fields []Field, build Build, confirm func(*mutate.Plan, Rebuild) ui.Modal) *Form {
	f := &Form{title: title, fields: fields, build: build, confirm: confirm, values: make([]string, len(fields))}
	for i, fl := range fields {
		f.values[i] = fl.Default
	}
	return f
}

type builtMsg struct {
	form *Form
	plan *mutate.Plan
	err  error
}

// maxField bounds typed text; ZFS names are at most 255 bytes.
const maxField = 255

// Busy implements ui.Modal: building only reads.
func (f *Form) Busy() bool { return false }

// Keys implements ui.Modal.
func (f *Form) Keys() []key.Binding {
	out := []key.Binding{keyEnterPreview}
	if !f.last() {
		skip := ui.SkipKey()
		out = []key.Binding{keyEnterNext, key.NewBinding(key.WithKeys(skip), key.WithHelp(skip, "see what it does"))}
	}
	out = append(out, keyNextField)
	if slices.ContainsFunc(f.fields, func(fl Field) bool { return fl.Switch }) {
		out = append(out, keySpaceToggle)
	}
	return append(out, keyEscCancel)
}

var (
	keyEnterPreview = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "see what it does"))
	keyEnterNext    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next field"))
	keyNextField    = key.NewBinding(key.WithKeys("tab", "down", "up"), key.WithHelp("tab/↓/↑", "field"))
	keySpaceToggle  = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "switch on/off"))
)

func (f *Form) valueMap() map[string]string {
	m := make(map[string]string, len(f.fields))
	for i, fl := range f.fields {
		m[fl.Name] = f.values[i]
	}
	return m
}

// Update implements ui.Modal.
func (f *Form) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case builtMsg:
		if msg.form != f {
			return f, nil
		}
		f.busy = false
		if msg.err != nil {
			f.err = msg.err.Error()
			return f, nil
		}
		if msg.plan.Secret { // looking again would need the passphrase again
			return f.confirm(msg.plan, nil), nil
		}
		values, build := f.valueMap(), f.build
		return f.confirm(msg.plan, func(ctx context.Context) (*mutate.Plan, error) { return build(ctx, values) }), nil
	case tea.KeyPressMsg:
		return f.key(msg)
	}
	return f, nil
}

func (f *Form) key(msg tea.KeyPressMsg) (ui.Modal, tea.Cmd) {
	if f.busy {
		return f, nil
	}
	fl := f.fields[f.focus]
	switch k := msg.String(); {
	case k == "esc":
		f.wipe()
		return nil, nil
	case k == "enter" && !f.last():
		// enter goes through every field, and on the last one shows the
		// change; ctrl+enter (alt+enter) shows it from any field. The
		// same in every form.
		f.focus++
	case k == "enter" || k == "ctrl+enter" || k == "alt+enter":
		if i := f.firstEmpty(); i >= 0 {
			f.focus, f.err = i, label(f.fields[i])+" is required."
			return f, nil
		}
		if f.need != nil {
			if miss := missing(f.need(f.valueMap()), f.perms); len(miss) > 0 {
				f.err = "Needs root or delegated " + strings.Join(miss, ", ") + " on " + termtext.New(f.on).Display() + "."
				return f, nil
			}
		}
		if f.buildSecret != nil {
			return f, f.startSecret()
		}
		f.busy, f.err = true, ""
		values, build := f.valueMap(), f.build
		return f, func() tea.Msg {
			p, err := build(context.Background(), values)
			return builtMsg{form: f, plan: p, err: err}
		}
	case k == "tab" || k == "down":
		f.focus = (f.focus + 1) % len(f.fields)
	case k == "shift+tab" || k == "up":
		f.focus = (f.focus + len(f.fields) - 1) % len(f.fields)
	case fl.Secret:
		f.secretKey(msg)
	case fl.Switch && k == "space":
		f.values[f.focus] = boolString(f.values[f.focus] != "true")
	case !fl.Switch && k == "backspace":
		if v := f.values[f.focus]; v != "" {
			_, n := utf8.DecodeLastRuneInString(v)
			f.values[f.focus] = v[:len(v)-n]
		}
	case !fl.Switch && msg.Text != "" && len(f.values[f.focus])+len(msg.Text) <= maxField:
		f.values[f.focus] += msg.Text
	}
	return f, nil
}

// label is a field's label without its note in brackets.
func label(fl Field) string {
	l, _, _ := strings.Cut(fl.Label, " (")
	return l
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Render implements ui.Modal.
func (f *Form) Render(d ui.Display, width, height int) string {
	w := min(width, maxModalWidth)
	iw, _ := ui.Inner(w, height)
	accent, muted := d.Palette.Style(theme.Accent).Bold(true), d.Palette.Style(theme.Muted)
	lines := []string{}
	for i, fl := range f.fields {
		v := termtext.New(f.values[i]).Display()
		switch {
		case fl.Secret:
			v = strings.Repeat("*", utf8.RuneCount(f.secrets[i]))
			if i == f.focus {
				v += ui.Cursor(d)
			}
		case fl.Switch:
			v = map[bool]string{true: "[x]", false: "[ ]"}[f.values[i] == "true"]
		case i == f.focus:
			v += ui.Cursor(d)
		}
		label := fl.Label
		if required(fl) {
			label += "*"
		}
		if i == f.focus {
			label = accent.Render(label)
		}
		lines = append(lines, fit(label+": "+v, iw))
	}
	lines = append(lines, "")
	switch {
	case f.busy:
		lines = append(lines, muted.Render("Planning…"))
	case f.err != "":
		for _, l := range strings.Split(f.err, "\n") {
			lines = append(lines, d.Palette.Style(theme.Error).Render(fit(sentence(safe(l)), iw)))
		}
	default:
		hint := "Nothing changes yet."
		if slices.ContainsFunc(f.fields, required) {
			hint = "* required. " + hint
		}
		lines = append(lines, muted.Render(hint))
	}
	return ui.Panel{Title: lipgloss.NewStyle().Render(f.title), Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

// startSecret builds with a copy of the passphrase and clears what was
// typed; a wrong or failed attempt is typed again.
func (f *Form) startSecret() tea.Cmd {
	secret, why := f.secret()
	f.wipe()
	if why != "" {
		f.err = why
		return nil
	}
	f.busy, f.err = true, ""
	values, build := f.valueMap(), f.buildSecret
	return func() tea.Msg {
		defer clear(secret)
		p, err := build(context.Background(), values, secret)
		return builtMsg{form: f, plan: p, err: err}
	}
}

// secretKey types into a passphrase field without reallocating it.
func (f *Form) secretKey(msg tea.KeyPressMsg) {
	s := f.secrets[f.focus]
	switch {
	case msg.String() == "backspace" && len(s) > 0:
		_, n := utf8.DecodeLastRune(s)
		clear(s[len(s)-n:])
		f.secrets[f.focus] = s[:len(s)-n]
	case msg.Text != "" && len(s)+len(msg.Text) <= cap(s):
		f.secrets[f.focus] = append(s, msg.Text...)
	}
}

// Typing implements ui.Typing: a text or passphrase field has focus, so
// pasted text goes to it.
func (f *Form) Typing() bool { return len(f.fields) > 0 && !f.fields[f.focus].Switch }

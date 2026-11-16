// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// importForm lists the pools zpool import finds and imports one. The
// first line is where to look (empty: zpool's usual places; enter looks
// again), then one line per pool, then the options.
type importForm struct {
	b       plan.Builder
	confirm func(*mutate.Plan, Rebuild) ui.Modal

	dir      string
	found    []zfs.Importable
	scanning bool
	busy     bool
	err      string

	chosen            int // index into found, -1 for none
	newName           string
	readOnly, noMount bool
	focus             int // 0 dir, 1..len(found) pools, then the options
}

type importScanMsg struct {
	f     *importForm
	found []zfs.Importable
	err   error
}

type importBuiltMsg struct {
	f    *importForm
	plan *mutate.Plan
	err  error
}

// The option rows after the pools.
const (
	importNewName = iota
	importReadOnly
	importNoMount
	importOptions
)

func openImportForm(b plan.Builder, confirm func(*mutate.Plan, Rebuild) ui.Modal) (ui.Modal, tea.Cmd) {
	f := &importForm{b: b, confirm: confirm, chosen: -1}
	return f, f.scan()
}

// scan runs zpool import (read-only: it reads the devices' labels).
func (f *importForm) scan() tea.Cmd {
	f.scanning, f.err, f.found, f.chosen = true, "", nil, -1
	b, dirs := f.b, f.dirs()
	return func() tea.Msg {
		found, err := b.R.Importable(context.Background(), dirs)
		return importScanMsg{f: f, found: found, err: err}
	}
}

func (f *importForm) dirs() []string {
	if d := strings.TrimSpace(f.dir); d != "" {
		return []string{d}
	}
	return nil
}

func (f *importForm) rows() int { return 1 + len(f.found) + importOptions }

// option is the option row the focus is on, or -1.
func (f *importForm) option() int {
	if i := f.focus - 1 - len(f.found); i >= 0 {
		return i
	}
	return -1
}

func (f *importForm) Busy() bool { return false }

func (f *importForm) Keys() []key.Binding {
	skip := ui.SkipKey()
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose / next")),
		key.NewBinding(key.WithKeys(skip), key.WithHelp(skip, "see what it does")),
		keyNextField, keySpaceToggle, keyEscCancel,
	}
}

func (f *importForm) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case importScanMsg:
		if msg.f == f {
			f.scanning = false
			if msg.err != nil {
				f.err = msg.err.Error()
			}
			f.found = msg.found
			if len(f.found) == 1 {
				f.chosen = 0
			}
		}
	case importBuiltMsg:
		if msg.f != f {
			return f, nil
		}
		f.busy = false
		if msg.err != nil {
			f.err = msg.err.Error()
			return f, nil
		}
		return f.confirm(msg.plan, f.rebuild()), nil
	case tea.KeyPressMsg:
		return f.key(msg)
	}
	return f, nil
}

func (f *importForm) key(msg tea.KeyPressMsg) (ui.Modal, tea.Cmd) {
	k := msg.String()
	switch {
	case k == "esc":
		return nil, nil
	case f.busy || f.scanning:
	case k == "tab" || k == "down":
		f.focus = (f.focus + 1) % f.rows()
	case k == "shift+tab" || k == "up":
		f.focus = (f.focus + f.rows() - 1) % f.rows()
	case k == "ctrl+enter" || k == "alt+enter" || (k == "enter" && f.focus == f.rows()-1):
		return f, f.build()
	case f.focus == 0 && k == "enter":
		return f, f.scan()
	case f.focus <= len(f.found) && f.focus > 0 && (k == "enter" || k == "space"):
		f.chosen = f.focus - 1
		f.focus = 1 + len(f.found)
	case f.option() == importReadOnly && (k == "space" || k == "enter"):
		f.readOnly = !f.readOnly
	case f.option() == importNoMount && (k == "space" || k == "enter"):
		f.noMount = !f.noMount
	case k == "enter":
		f.focus++
	case f.focus == 0:
		editText(&f.dir, msg)
	case f.option() == importNewName:
		editText(&f.newName, msg)
	}
	return f, nil
}

// editText edits a text field: backspace, or printable text up to maxField.
func editText(s *string, msg tea.KeyPressMsg) {
	switch {
	case msg.String() == "backspace" && *s != "":
		_, n := utf8.DecodeLastRuneInString(*s)
		*s = (*s)[:len(*s)-n]
	case msg.Text != "" && len(*s)+len(msg.Text) <= maxField:
		*s += msg.Text
	}
}

func (f *importForm) spec() plan.ImportSpec {
	s := plan.ImportSpec{NewName: strings.TrimSpace(f.newName), ReadOnly: f.readOnly, NoMount: f.noMount, Dirs: f.dirs()}
	if f.chosen >= 0 && f.chosen < len(f.found) {
		s.ID = f.found[f.chosen].ID
	}
	return s
}

func (f *importForm) rebuild() Rebuild {
	b, s := f.b, f.spec()
	return func(ctx context.Context) (*mutate.Plan, error) { return b.PoolImport(ctx, s) }
}

func (f *importForm) build() tea.Cmd {
	if f.chosen < 0 {
		f.err = "Choose a pool: enter on its line."
		return nil
	}
	f.busy, f.err = true, ""
	run := f.rebuild()
	return func() tea.Msg {
		p, err := run(context.Background())
		return importBuiltMsg{f: f, plan: p, err: err}
	}
}

func (f *importForm) Render(d ui.Display, width, height int) string {
	w := min(width, maxModalWidth)
	iw, _ := ui.Inner(w, height)
	accent, muted := d.Palette.Style(theme.Accent).Bold(true), d.Palette.Style(theme.Muted)
	label := func(row int, s string) string {
		if row == f.focus {
			return accent.Render(s)
		}
		return s
	}
	text := func(row int, v string) string {
		v = termtext.New(v).Display()
		if row == f.focus {
			v += ui.Cursor(d)
		}
		return v
	}
	lines := []string{label(0, "Look in (empty: the usual /dev places; enter looks again)") + ": " + text(0, f.dir), ""}
	switch {
	case f.scanning:
		lines = append(lines, muted.Render("Looking for pools…"))
	case len(f.found) == 0:
		lines = append(lines, muted.Render("No pool to import was found."))
	}
	for i, p := range f.found {
		mark := "  "
		if i == f.chosen {
			mark = "> "
		}
		lines = append(lines, fit(label(1+i, mark+p.Name.Display()+"  "+p.State+"  id "+p.ID), iw))
		if i == f.chosen {
			lines = append(lines, f.details(d, p, iw)...)
		}
	}
	base := 1 + len(f.found)
	onOff := map[bool]string{true: "on", false: "off"}
	lines = append(lines, "",
		label(base+importNewName, "Import under a new name (empty: keep it)")+": "+text(base+importNewName, f.newName),
		label(base+importReadOnly, "Read-only (nothing is written)")+": "+onOff[f.readOnly],
		label(base+importNoMount, "Do not mount its filesystems")+": "+onOff[f.noMount], "")
	switch {
	case f.busy:
		lines = append(lines, muted.Render("Planning…"))
	case f.err != "":
		lines = append(lines, d.Palette.Style(theme.Error).Render(ansi.Wrap(sentence(safe(f.err)), iw, " ")))
	default:
		lines = append(lines, muted.Render("Looking reads only the disks' labels. Nothing changes yet."))
	}
	return ui.Panel{Title: "Import a pool", Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

// details are the chosen pool's explanation and devices, as zpool
// import printed them.
func (f *importForm) details(d ui.Display, p zfs.Importable, iw int) []string {
	muted, warn := d.Palette.Style(theme.Muted), d.Palette.Style(theme.Warn)
	var out []string
	for _, s := range p.Status {
		out = append(out, warn.Render(fit("    "+s.Display(), iw)))
	}
	for _, dev := range p.Devices {
		line := "    " + strings.Repeat("  ", dev.Depth) + dev.Name.Display() + "  " + dev.State
		if !dev.Note.IsEmpty() {
			line += "  " + dev.Note.Display()
		}
		out = append(out, muted.Render(fit(line, iw)))
	}
	return out
}

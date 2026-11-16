// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// pickForm chooses the one device to attach to a pool's device or to
// replace it with: enter on a device plans it; the last line switches
// overwrite on for a device that holds something.
type pickForm struct {
	b       plan.Builder
	op      mutate.Op // DeviceAttach or DeviceReplace
	pool    string
	dev     string
	confirm func(*mutate.Plan, Rebuild) ui.Modal

	deviceList
	loading, busy bool
	err           string
	cursor        int // a usable device, or len(usable) for overwrite
	overwrite     bool
}

type pickDevicesMsg struct {
	f    *pickForm
	devs []disks.Device
	err  error
}

type pickBuiltMsg struct {
	f    *pickForm
	plan *mutate.Plan
	err  error
}

func openPickForm(b plan.Builder, op mutate.Op, pool, dev string, confirm func(*mutate.Plan, Rebuild) ui.Modal) (ui.Modal, tea.Cmd) {
	f := &pickForm{b: b, op: op, pool: pool, dev: dev, confirm: confirm, loading: true}
	return f, func() tea.Msg {
		if b.Devices == nil {
			return pickDevicesMsg{f: f, err: plan.ErrNoInventory}
		}
		devs, err := b.Devices(context.Background())
		return pickDevicesMsg{f: f, devs: devs, err: err}
	}
}

func (f *pickForm) Busy() bool { return false }

func (f *pickForm) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "use this device")),
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "device")),
		key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "switch overwrite")), keyEscCancel,
	}
}

func (f *pickForm) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case pickDevicesMsg:
		if msg.f == f {
			f.loading = false
			if msg.err != nil {
				f.err = msg.err.Error()
			} else {
				f.deviceList = newDeviceList(msg.devs)
			}
		}
	case pickBuiltMsg:
		if msg.f != f {
			return f, nil
		}
		f.busy = false
		if msg.err != nil {
			f.err = msg.err.Error()
			return f, nil
		}
		return f.confirm(msg.plan, f.rebuild(msg.plan.Params["new"])), nil
	case tea.KeyPressMsg:
		return f.key(msg)
	}
	return f, nil
}

func (f *pickForm) key(msg tea.KeyPressMsg) (ui.Modal, tea.Cmd) {
	switch k := msg.String(); {
	case k == "esc":
		return nil, nil
	case f.busy || f.loading:
	case k == "down" || k == "tab" || k == "j":
		f.cursor = min(f.cursor+1, len(f.usable))
	case k == "up" || k == "shift+tab" || k == "k":
		f.cursor = max(f.cursor-1, 0)
	case k == "space" || (k == "enter" && f.cursor == len(f.usable)):
		f.overwrite = !f.overwrite
	case k == "enter":
		f.busy, f.err = true, ""
		run := f.rebuild(f.usable[f.cursor].ByID)
		return f, func() tea.Msg {
			p, err := run(context.Background())
			return pickBuiltMsg{f: f, plan: p, err: err}
		}
	}
	return f, nil
}

func (f *pickForm) rebuild(dev string) Rebuild {
	b, s := f.b, plan.NewDeviceSpec{Pool: f.pool, Device: f.dev, New: dev, Overwrite: f.overwrite}
	op := f.op
	return func(ctx context.Context) (*mutate.Plan, error) { return b.DeviceNew(ctx, op, s) }
}

func (f *pickForm) Render(d ui.Display, width, height int) string {
	w := min(width, maxModalWidth)
	iw, _ := ui.Inner(w, height)
	accent, muted := d.Palette.Style(theme.Accent).Bold(true), d.Palette.Style(theme.Muted)
	title := "Attach a device to " + termtext.New(f.dev).Display()
	if f.op == mutate.DeviceReplace {
		title = "Replace " + termtext.New(f.dev).Display()
	}
	var lines []string
	switch {
	case f.loading:
		lines = append(lines, muted.Render("Reading the disks…"))
	case len(f.usable) == 0 && f.err == "":
		lines = append(lines, muted.Render("No disk or partition can be used."))
	default:
		lines = append(lines, muted.Render("The new device:"))
		for i, dev := range f.usable {
			line := "  " + f.describe(d, dev)
			if i == f.cursor {
				line = accent.Render("> ") + f.describe(d, dev)
			}
			lines = append(lines, fit(line, iw))
		}
		label := "Overwrite a device that holds something"
		if f.cursor == len(f.usable) {
			label = accent.Render(label)
		}
		lines = append(lines, "", label+": "+map[bool]string{true: "on", false: "off"}[f.overwrite])
	}
	lines = append(lines, f.refusedLines(d, iw)...)
	lines = append(lines, "")
	switch {
	case f.busy:
		lines = append(lines, muted.Render("Planning…"))
	case f.err != "":
		lines = append(lines, d.Palette.Style(theme.Error).Render(ansi.Wrap(sentence(safe(f.err)), iw, " ")))
	default:
		lines = append(lines, muted.Render("Nothing changes yet."))
	}
	return ui.Panel{Title: title, Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

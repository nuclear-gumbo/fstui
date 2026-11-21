// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/format"

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

// The choices a pool form cycles through with space.
var (
	poolLayouts      = []string{"", "mirror", "raidz1", "raidz2", "raidz3"}
	poolRoles        = []string{"", "data", "log", "cache", "special", "spare"}
	poolAshifts      = []string{"", "12", "9", "13", "14"}
	poolCompressions = []string{"", "lz4", "zstd", "off"}
)

// poolRow is one line of the form the cursor can be on.
type poolRow int

const (
	rowName poolRow = iota
	rowLayout
	rowDevice // one per usable device
	rowAshift
	rowCompression
	rowAutotrim
	rowOverwrite
	rowMountpoint
	rowCheckpoint
)

// formRow is a line the cursor can be on; dev is the device's index for
// rowDevice.
type formRow struct {
	kind poolRow
	dev  int
}

// poolForm asks for a new pool (name, layout, a role for each usable
// device, options) and builds it with plan.PoolCreate; or, with pool
// set, for devices to add to that pool, built with plan.PoolAdd. It
// hands the plan to the confirmation like Form does.
type poolForm struct {
	b       plan.Builder
	confirm func(*mutate.Plan, Rebuild) ui.Modal
	pool    string // adding to this pool; empty when creating one
	loading bool
	err     string
	busy    bool

	deviceList
	roles []int // index into poolRoles, per usable device

	name, mountpoint            string
	layout, ashift, compression int
	autotrim, overwrite         bool
	checkpoint                  bool
	focus                       int // index into rows()
}

type devicesMsg struct {
	f      *poolForm
	devs   []disks.Device
	layout string // the pool's data layout, when adding
	err    error
}

type poolBuiltMsg struct {
	f    *poolForm
	plan *mutate.Plan
	err  error
}

// openPoolForm opens the form for a new pool, or for devices to add to
// pool, and reads the devices (and the pool's layout).
func openPoolForm(b plan.Builder, pool string, confirm func(*mutate.Plan, Rebuild) ui.Modal) (ui.Modal, tea.Cmd) {
	f := &poolForm{b: b, confirm: confirm, pool: pool, loading: true, layout: 1}
	return f, func() tea.Msg {
		if b.Devices == nil {
			return devicesMsg{f: f, err: plan.ErrNoInventory}
		}
		ctx := context.Background()
		devs, err := b.Devices(ctx)
		msg := devicesMsg{f: f, devs: devs, err: err}
		if err == nil && pool != "" {
			st, err := b.R.PoolTopology(ctx, pool)
			if err != nil {
				return devicesMsg{f: f, err: err}
			}
			if l := plan.DataLayouts(st); len(l) == 1 {
				msg.layout = l[0]
			}
		}
		return msg
	}
}

// rows lists the form's lines in order.
func (f *poolForm) rows() []formRow {
	var out []formRow
	if f.pool == "" {
		out = append(out, formRow{kind: rowName})
	}
	out = append(out, formRow{kind: rowLayout})
	for i := range f.usable {
		out = append(out, formRow{rowDevice, i})
	}
	tail := []poolRow{rowAshift, rowCompression, rowAutotrim, rowOverwrite, rowMountpoint}
	if f.pool != "" {
		tail = []poolRow{rowAshift, rowOverwrite, rowCheckpoint}
	}
	for _, k := range tail {
		out = append(out, formRow{kind: k})
	}
	return out
}

func (f *poolForm) Busy() bool { return false }

func (f *poolForm) Keys() []key.Binding {
	skip := ui.SkipKey()
	return []key.Binding{
		keyEnterNext, key.NewBinding(key.WithKeys(skip), key.WithHelp(skip, "see what it does")),
		keyNextField, key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "next choice")), keyEscCancel,
	}
}

func (f *poolForm) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case devicesMsg:
		if msg.f == f {
			f.loading = false
			if msg.err != nil {
				f.err = msg.err.Error()
				return f, nil
			}
			f.sort(msg.devs)
			if i := slices.Index(poolLayouts, msg.layout); f.pool != "" && i >= 0 {
				f.layout = i
			}
		}
	case poolBuiltMsg:
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

// sort splits the inventory into devices a pool may use and the rest.
func (f *poolForm) sort(devs []disks.Device) {
	f.deviceList = newDeviceList(devs)
	f.roles = make([]int, len(f.usable))
}

// deviceList is the inventory as a form shows it: the devices a pool may
// use, and the rest with the reason.
type deviceList struct {
	usable  []disks.Device // free, or holding a signature
	refused []disks.Device
	why     map[string]string // by by-id path: the signature or why it is refused
}

func newDeviceList(devs []disks.Device) deviceList {
	l := deviceList{why: map[string]string{}}
	for _, d := range devs {
		if d.Type != "disk" && d.Type != "part" {
			continue // a CD drive or a loop device is not worth a line
		}
		v, why := disks.Judge(d, devs)
		l.why[d.ByID] = why
		if v == disks.Refused {
			l.refused = append(l.refused, d)
		} else {
			l.usable = append(l.usable, d)
		}
	}
	return l
}

// describe is one device: name, size, model and what it holds.
func (l deviceList) describe(d ui.Display, dev disks.Device) string {
	line := deviceName(dev) + "  " + format.Size(dev.Size, d.Exact)
	if dev.Model != "" {
		line += "  " + termtext.New(dev.Model).Display()
	}
	if why := l.why[dev.ByID]; why != "" {
		line += "  " + d.Palette.Style(theme.Warn).Render(why)
	}
	return line
}

// refusedLines list the devices that cannot be used, with the reason.
func (l deviceList) refusedLines(d ui.Display, iw int) []string {
	if len(l.refused) == 0 {
		return nil
	}
	muted := d.Palette.Style(theme.Muted)
	out := []string{"", muted.Render("Not usable")}
	for _, dev := range l.refused {
		out = append(out, muted.Render(fit(deviceName(dev)+"  "+l.why[dev.ByID], iw)))
	}
	return out
}

func (f *poolForm) key(msg tea.KeyPressMsg) (ui.Modal, tea.Cmd) {
	if f.busy {
		return f, nil
	}
	rows := f.rows()
	r := rows[min(f.focus, len(rows)-1)]
	switch k := msg.String(); {
	case k == "esc":
		return nil, nil
	case f.loading:
	case k == "enter" && f.focus < len(rows)-1:
		f.focus++
	case k == "enter" || k == "ctrl+enter" || k == "alt+enter":
		return f, f.build()
	case k == "tab" || k == "down":
		f.focus = (f.focus + 1) % len(rows)
	case k == "shift+tab" || k == "up":
		f.focus = (f.focus + len(rows) - 1) % len(rows)
	case k == "space":
		f.cycle(r.kind, r.dev)
	case r.kind == rowName:
		editText(&f.name, msg)
	case r.kind == rowMountpoint:
		editText(&f.mountpoint, msg)
	}
	return f, nil
}

func (f *poolForm) cycle(kind poolRow, dev int) {
	next := func(i, n int) int { return (i + 1) % n }
	switch kind { //nolint:exhaustive // text rows do not cycle
	case rowLayout:
		f.layout = next(f.layout, len(poolLayouts))
	case rowDevice:
		f.roles[dev] = next(f.roles[dev], len(poolRoles))
	case rowAshift:
		f.ashift = next(f.ashift, len(poolAshifts))
	case rowCompression:
		f.compression = next(f.compression, len(poolCompressions))
	case rowAutotrim:
		f.autotrim = !f.autotrim
	case rowOverwrite:
		f.overwrite = !f.overwrite
	case rowCheckpoint:
		f.checkpoint = !f.checkpoint
	}
}

// spec is the pool the form describes.
func (f *poolForm) spec() plan.PoolSpec {
	s := plan.PoolSpec{
		Name: f.name, Layout: poolLayouts[f.layout], Ashift: poolAshifts[f.ashift],
		Compression: poolCompressions[f.compression], Mountpoint: f.mountpoint,
		Autotrim: f.autotrim, Overwrite: f.overwrite,
	}
	for i, d := range f.usable {
		switch poolRoles[f.roles[i]] {
		case "data":
			s.Data = append(s.Data, d.ByID)
		case "log":
			s.Log = append(s.Log, d.ByID)
		case "cache":
			s.Cache = append(s.Cache, d.ByID)
		case "special":
			s.Special = append(s.Special, d.ByID)
		case "spare":
			s.Spare = append(s.Spare, d.ByID)
		}
	}
	return s
}

// addSpec is the devices the form adds to its pool.
func (f *poolForm) addSpec() plan.AddSpec {
	s := f.spec()
	return plan.AddSpec{
		Pool: f.pool, Layout: s.Layout, Data: s.Data, Log: s.Log, Cache: s.Cache, Special: s.Special, Spare: s.Spare,
		Ashift: s.Ashift, Overwrite: s.Overwrite, Checkpoint: f.checkpoint,
	}
}

// rebuild plans what the form describes, now as filled in.
func (f *poolForm) rebuild() Rebuild {
	b := f.b
	if f.pool != "" {
		s := f.addSpec()
		return func(ctx context.Context) (*mutate.Plan, error) { return b.PoolAdd(ctx, s) }
	}
	s := f.spec()
	return func(ctx context.Context) (*mutate.Plan, error) { return b.PoolCreate(ctx, s) }
}

func (f *poolForm) build() tea.Cmd {
	s := f.spec()
	switch {
	case f.pool == "" && s.Name == "":
		f.err, f.focus = "Name is required.", 0
		return nil
	case f.pool == "" && len(s.Data) == 0:
		f.err = "Choose at least one data device: space on a device sets its role."
		return nil
	case len(s.Data)+len(s.Log)+len(s.Cache)+len(s.Special)+len(s.Spare) == 0:
		f.err = "Choose at least one device: space on a device sets its role."
		return nil
	}
	f.busy, f.err = true, ""
	run := f.rebuild()
	return func() tea.Msg {
		p, err := run(context.Background())
		return poolBuiltMsg{f: f, plan: p, err: err}
	}
}

func (f *poolForm) Render(d ui.Display, width, height int) string {
	w := min(width, maxModalWidth)
	iw, _ := ui.Inner(w, height)
	muted := d.Palette.Style(theme.Muted)
	var lines []string
	if f.loading {
		lines = append(lines, muted.Render("Reading the disks…"))
	} else {
		lines = f.formLines(d, iw)
	}
	lines = append(lines, "")
	switch {
	case f.busy:
		lines = append(lines, muted.Render("Planning…"))
	case f.err != "":
		lines = append(lines, d.Palette.Style(theme.Error).Render(ansi.Wrap(sentence(safe(f.err)), iw, " ")))
	case f.pool != "":
		lines = append(lines, muted.Render("Nothing changes yet."))
	default:
		lines = append(lines, muted.Render("* required. Nothing changes yet."))
	}
	title := "Create a new pool"
	if f.pool != "" {
		title = "Add devices to " + termtext.New(f.pool).Display()
	}
	return ui.Panel{Title: title, Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

func (f *poolForm) formLines(d ui.Display, iw int) []string {
	var out []string
	for i, r := range f.rows() {
		out = append(out, f.rowLines(d, iw, i, r.kind, r.dev)...)
	}
	return append(out, f.refusedLines(d, iw)...)
}

// rowLines draws row i of the form, of kind (and device dev).
func (f *poolForm) rowLines(d ui.Display, iw, i int, kind poolRow, dev int) []string {
	label := func(s string) string {
		if i == f.focus {
			return d.Palette.Style(theme.Accent).Bold(true).Render(s)
		}
		return s
	}
	text := func(v string) string {
		v = termtext.New(v).Display()
		if i == f.focus {
			v += ui.Cursor(d)
		}
		return v
	}
	onOff := map[bool]string{true: "on", false: "off"}
	switch kind {
	case rowName:
		return []string{label("Name*") + ": " + text(f.name)}
	case rowLayout:
		head := "Devices: space sets the role (data, log, cache, special, spare)"
		if len(f.usable) == 0 {
			head = "No disk or partition can be used for a pool."
		}
		return []string{label("Layout") + ": " + orDefault(poolLayouts[f.layout], "stripe (no redundancy)"), "", d.Palette.Style(theme.Muted).Render(head)}
	case rowDevice:
		return []string{fit(label(f.deviceLine(d, dev)), iw)}
	case rowAshift:
		return []string{"", label("ashift") + ": " + orDefault(poolAshifts[f.ashift], "automatic")}
	case rowCompression:
		return []string{label("Compression") + ": " + orDefault(poolCompressions[f.compression], "default (lz4)")}
	case rowAutotrim:
		return []string{label("autotrim (SSDs)") + ": " + onOff[f.autotrim]}
	case rowOverwrite:
		return []string{label("Overwrite devices that hold something") + ": " + onOff[f.overwrite]}
	case rowMountpoint:
		return []string{label("Mountpoint (leave empty for /name)") + ": " + text(f.mountpoint)}
	case rowCheckpoint:
		return []string{label("Take a checkpoint first (to rewind if this goes wrong)") + ": " + onOff[f.checkpoint]}
	}
	return nil
}

// deviceLine is one usable device: its role, name, size, model and
// what it holds.
func (f *poolForm) deviceLine(d ui.Display, i int) string {
	return fmt.Sprintf("%-7s %s", orDefault(poolRoles[f.roles[i]], "-"), f.describe(d, f.usable[i]))
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// deviceName is the by-id name without the directory, which every
// device shares.
func deviceName(d disks.Device) string {
	return termtext.New(strings.TrimPrefix(d.ByID, "/dev/disk/by-id/")).Display()
}

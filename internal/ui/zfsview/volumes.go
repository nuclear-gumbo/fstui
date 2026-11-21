// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// VolumeSource reads every volume. *zfs.Reader implements it.
type VolumeSource interface {
	AllVolumes(ctx context.Context) ([]zfs.Dataset, error)
}

type allVolumes []zfs.Dataset

// Volumes lists every zvol: size, block size, sparse or thick, and its block
// device.
type Volumes struct {
	poll   poller[allVolumes]
	cursor listCursor
}

// NewVolumes returns the Volumes view, polled at refresh.datasets.
func NewVolumes(src VolumeSource, settings *config.Config, now func() time.Time) *Volumes {
	settings = settingsOr(settings)
	read := func(ctx context.Context) (allVolumes, error) {
		v, err := src.AllVolumes(ctx)
		return allVolumes(v), err
	}
	return &Volumes{poll: poller[allVolumes]{read: read, interval: settings.Refresh.Datasets, now: now, slow: true, limits: &settings.Limits}}
}

// Name implements ui.Screen.
func (v *Volumes) Name() string { return "Volumes" }

// Number implements ui.Numbered.
func (v *Volumes) Number() int { return volumesView }

// Keys implements ui.Screen.
func (v *Volumes) Keys() []key.Binding {
	return append([]key.Binding{keys.Get(keys.Refresh)}, listKeys()...)
}

// Nested implements ui.Screen.
func (v *Volumes) Nested() bool { return false }

// Init implements ui.Screen.
func (v *Volumes) Init() tea.Cmd { return v.poll.start() }

// Update implements ui.Screen.
func (v *Volumes) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	if cmd, updated := v.poll.handle(msg); cmd != nil || updated {
		v.cursor.clamp(len(v.poll.data))
		return v, cmd
	}
	if v.cursor.mouse(msg, len(v.poll.data), 1) { // below the header
		return v, nil
	}
	if c, ok := msg.(ui.ConfigMsg); ok {
		v.poll.interval = c.Config.Refresh.Datasets
		return v, nil
	}
	if _, ok := msg.(ui.ChangedMsg); ok {
		return v, v.poll.refresh()
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		if v.cursor.move(k, len(v.poll.data)) {
			return v, nil
		}
		if key.Matches(k, keys.Get(keys.Refresh)) {
			return v, v.poll.refresh()
		}
	}
	return v, nil
}

const (
	blockWidth  = len("16.0 KiB") // volblocksize up to 128 KiB
	sparseWidth = len("sparse")
)

// Render implements ui.Screen.
func (v *Volumes) Render(disp ui.Display, width, height int) string {
	muted := disp.Palette.Style(theme.Muted)
	switch {
	case v.poll.loaded.IsZero() && v.poll.lastErr.IsEmpty():
		return muted.Render("Reading volumes")
	case v.poll.loaded.IsZero():
		return "Could not read volumes\n" + v.poll.lastErr.Display() + "\n" + muted.Render("fstui retries automatically; r retries now.")
	case len(v.poll.data) == 0:
		return "No volumes."
	}
	nameCol := len("NAME")
	for _, d := range v.poll.data {
		nameCol = max(nameCol, len(d.Name))
	}
	fixed := markerWidth + 3*(gap+sizeWidth) + gap + blockWidth + gap + sparseWidth + gap + encWidth + gap
	nameCol = min(nameCol, max(width-fixed-len("/dev/zvol/"), len("NAME")))
	devCol := max(width-fixed-nameCol, 0)
	head := strings.Repeat(" ", markerWidth) + pad("NAME", nameCol) + sp + lpad("SIZE", sizeWidth) + sp +
		lpad("USED", sizeWidth) + sp + lpad("REFER", sizeWidth) + sp + lpad("BLOCK", blockWidth) + sp +
		pad("ALLOC", sparseWidth) + sp + pad("ENC", encWidth)
	if devCol > 0 {
		head += sp + "DEVICE"
	}
	lines := []string{muted.Render(head)}
	start, end := v.cursor.window(len(v.poll.data), max(height-1, 1))
	for i := start; i < end; i++ {
		lines = append(lines, volumeRow(disp, v.poll.data[i], i == v.cursor.i, nameCol, devCol))
	}
	return strings.Join(lines, "\n")
}

// volumeRow draws one volume; devCol is the room for its device path, cut
// from the left so the volume's own name stays (0: no device column).
func volumeRow(disp ui.Display, d zfs.Dataset, selected bool, nameCol, devCol int) string {
	pal := disp.Palette
	num := func(prop string) uint64 { n, _ := d.Properties.Number(prop); return n }
	marker, label := "  ", pad(leftTrunc(d.Name, nameCol), nameCol)
	if selected {
		marker = pal.Style(theme.Accent).Render(">") + " "
		label = lipgloss.NewStyle().Bold(true).Render(label)
	}
	// A sparse volume (zfs create -s) has no refreservation; a thick one
	// reserves its full size up front (zfsprops(7), volsize).
	alloc := "thick"
	if num("refreservation") == 0 {
		alloc = "sparse"
	}
	row := marker + label + sp + lpad(format.Size(num("volsize"), disp.Exact), sizeWidth) + sp +
		lpad(format.Size(num("used"), disp.Exact), sizeWidth) + sp + lpad(format.Size(num("referenced"), disp.Exact), sizeWidth) + sp +
		lpad(format.Size(num("volblocksize"), disp.Exact), blockWidth) + sp + pad(alloc, sparseWidth) + sp + pad(encryption(disp, d), encWidth)
	if devCol > 0 {
		row += sp + pal.Style(theme.Muted).Render(leftTrunc(zfs.ZvolDevice(d.Name), devCol))
	}
	return row
}

// Status implements ui.Screen.
func (v *Volumes) Status() ui.Status {
	st := ui.Status{Refreshed: v.poll.loaded, LastError: v.poll.lastErr}
	if !v.poll.loaded.IsZero() {
		st.Figure = fmt.Sprintf("%d %s", len(v.poll.data), format.Plural(len(v.poll.data), "volume", "volumes"))
	}
	return st
}

// Selection implements ui.Selector: the volume under the cursor.
func (v *Volumes) Selection() (ui.Target, bool) {
	if len(v.poll.data) == 0 {
		return ui.Target{}, false
	}
	return ui.Target{Backend: "zfs", Kind: "volume", Names: []string{v.poll.data[v.cursor.i].Name}}, true
}

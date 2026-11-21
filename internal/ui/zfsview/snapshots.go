// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"slices"
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

// SnapshotSource reads every snapshot and what changed since one.
// *zfs.Reader implements it.
type SnapshotSource interface {
	AllSnapshots(ctx context.Context) ([]zfs.SnapshotRow, error)
	Differ
	Sender
}

// allSnapshots is the global list's poller type; distinct from the other
// []zfs.Dataset pollers so results cannot cross views.
type allSnapshots []zfs.SnapshotRow

// Snapshots lists every snapshot on the system. It reads when first shown and on
// r, never on a timer: there can be hundreds of thousands.
type Snapshots struct {
	src     SnapshotSource
	marked  marks
	poll    poller[allSnapshots]
	cursor  listCursor
	sortBy  int
	reverse bool
	now     func() time.Time
	order   derived[listKey, []zfs.SnapshotRow]
}

// NewSnapshots returns the global Snapshots view.
func NewSnapshots(src SnapshotSource, settings *config.Config, now func() time.Time) *Snapshots {
	settings = settingsOr(settings)
	read := func(ctx context.Context) (allSnapshots, error) {
		s, err := src.AllSnapshots(ctx)
		return allSnapshots(s), err
	}
	// The interval only spaces retries after a failure; success schedules
	// no further reads because handle's tick is ignored below.
	return &Snapshots{src: src, marked: marks{}, poll: poller[allSnapshots]{read: read, interval: time.Minute, now: now, slow: true, limits: &settings.Limits}, now: now}
}

// snapshotListTop: the summary line and the column header come first.
const snapshotListTop = 2

// Name implements ui.Screen.
func (v *Snapshots) Name() string { return "Snapshots" }

// Keys implements ui.Screen.
func (v *Snapshots) Keys() []key.Binding {
	return append([]key.Binding{keys.Get(keys.SendSize), keys.Get(keys.Diff), keys.Get(keys.Mark), keys.Get(keys.ClearMarks), keys.Get(keys.Sort), keys.Get(keys.Reverse), keys.Get(keys.Refresh)}, listKeys()...)
}

// Nested implements ui.Screen.
func (v *Snapshots) Nested() bool { return false }

// Init implements ui.Screen; nothing is read until the view is shown.
func (v *Snapshots) Init() tea.Cmd { return nil }

// Number implements ui.Numbered.
func (v *Snapshots) Number() int { return snapshotsView }

// Activate implements ui.Activator: the first visit reads the list.
func (v *Snapshots) Activate() tea.Cmd {
	if v.poll.loaded.IsZero() && v.poll.lastErr.IsEmpty() {
		return v.poll.start()
	}
	return nil
}

// Update implements ui.Screen.
func (v *Snapshots) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	if _, ok := msg.(pollDue[allSnapshots]); ok && v.poll.lastErr.IsEmpty() {
		return v, nil // after a success, only r reads again
	}
	if _, ok := msg.(ui.ChangedMsg); ok {
		clear(v.marked) // what they named may be gone
		if v.poll.loaded.IsZero() {
			return v, nil // never shown: nothing to bring up to date
		}
		return v, v.poll.refresh()
	}
	if cmd, updated := v.poll.handle(msg); cmd != nil || updated {
		v.cursor.clamp(len(v.poll.data))
		v.marked.keep(func(n string) bool {
			return slices.ContainsFunc(v.poll.data, func(d zfs.SnapshotRow) bool { return d.Name == n })
		})
		return v, cmd
	}
	if v.cursor.mouse(msg, len(v.poll.data), snapshotListTop) {
		return v, nil
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return v, nil
	}
	switch {
	case v.cursor.move(k, len(v.poll.data)):
	case key.Matches(k, keys.Get(keys.Sort)):
		v.sortBy = (v.sortBy + 1) % numSorts
	case key.Matches(k, keys.Get(keys.Reverse)):
		v.reverse = !v.reverse
	case key.Matches(k, keys.Get(keys.Refresh)):
		return v, v.poll.refresh()
	case key.Matches(k, keys.Get(keys.Mark)) && len(v.poll.data) > 0:
		v.marked.toggle(v.sorted()[v.cursor.i].Name)
		v.cursor.i = min(v.cursor.i+1, len(v.poll.data)-1) // on to the next, as file managers do
	case key.Matches(k, keys.Get(keys.ClearMarks)):
		clear(v.marked)
	case key.Matches(k, keys.Get(keys.Diff)) && len(v.poll.data) > 0:
		return v, openDiff(v.src, v.sorted()[v.cursor.i].Name)
	case key.Matches(k, keys.Get(keys.SendSize)) && len(v.poll.data) > 0:
		return v, openSend(v.src, v.sendSet())
	}
	return v, nil
}

// Render implements ui.Screen.
func (v *Snapshots) Render(disp ui.Display, width, height int) string {
	muted := disp.Palette.Style(theme.Muted)
	switch {
	case v.poll.loaded.IsZero() && v.poll.lastErr.IsEmpty():
		return muted.Render("Reading every snapshot: zfs takes about half a second per thousand")
	case v.poll.loaded.IsZero():
		return "Could not read snapshots\n" + v.poll.lastErr.Display() + "\n" + muted.Render("r retries.")
	case len(v.poll.data) == 0:
		return "No snapshots."
	}
	list := v.sorted()
	nameCol := len("SNAPSHOT")
	for _, s := range list {
		nameCol = max(nameCol, len(s.Name))
	}
	fixed := markerWidth + len("2006-01-02 15:04") + 2*sizeWidth + len("HOLDS") + len("CLONES") + 5*gap
	nameCol = min(nameCol, max(width-fixed, len("SNAPSHOT")))
	order := "ascending"
	if v.reverse {
		order = "descending"
	}
	lines := []string{
		muted.Render(fmt.Sprintf("%d %s, sorted by %s, %s; loaded once, r reloads", len(list), format.Plural(len(list), "snapshot", "snapshots"), sortNames[v.sortBy], order)),
		muted.Render(strings.Repeat(" ", markerWidth) + snapshotHeader(nameCol)),
	}
	start, end := v.cursor.window(len(list), max(height-len(lines), 1))
	for i := start; i < end; i++ {
		marker := rowMarker(disp, i == v.cursor.i, v.marked[list[i].Name])
		row := snapshotRow(disp, list[i], leftTrunc(list[i].Name, nameCol), nameCol, v.now())
		if i == v.cursor.i {
			row = lipgloss.NewStyle().Bold(true).Render(row)
		}
		lines = append(lines, marker+row)
	}
	return strings.Join(lines, "\n")
}

// Status implements ui.Screen.
func (v *Snapshots) Status() ui.Status {
	st := ui.Status{Refreshed: v.poll.loaded, LastError: v.poll.lastErr}
	if !v.poll.loaded.IsZero() {
		st.Figure = fmt.Sprintf("%d %s", len(v.poll.data), format.Plural(len(v.poll.data), "snapshot", "snapshots"))
	}
	return st
}

// sendSet is what e estimates: the marked snapshots, or the one under
// the cursor when none is marked.
func (v *Snapshots) sendSet() []zfs.SnapshotRow {
	if len(v.marked) == 0 {
		return []zfs.SnapshotRow{v.sorted()[v.cursor.i]}
	}
	var out []zfs.SnapshotRow
	for _, d := range v.poll.data {
		if v.marked[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// sorted is the list in the order shown, which the cursor indexes.
func (v *Snapshots) sorted() []zfs.SnapshotRow {
	return v.order.get(listKey{v.poll.loaded, len(v.poll.data), v.sortBy, v.reverse}, func() []zfs.SnapshotRow {
		return sortSnapshots(v.poll.data, v.sortBy, v.reverse)
	})
}

// Selection implements ui.Selector: the snapshot under the cursor.
func (v *Snapshots) Selection() (ui.Target, bool) {
	if len(v.poll.data) == 0 {
		return ui.Target{}, false
	}
	if len(v.marked) > 0 {
		return ui.Target{Backend: "zfs", Kind: "snapshot", Names: v.marked.sorted()}, true
	}
	return ui.Target{Backend: "zfs", Kind: "snapshot", Names: []string{v.sorted()[v.cursor.i].Name}}, true
}

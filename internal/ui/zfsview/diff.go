// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// Differ reads what changed since a snapshot; *zfs.Reader is one.
type Differ interface {
	Diff(ctx context.Context, from, to string) ([]zfs.DiffEntry, bool, error)
}

// diffWindow shows what changed in a dataset since one of its
// snapshots (zfs diff). It only reads.
type diffWindow struct {
	snap      string
	entries   []zfs.DiffEntry
	truncated bool
	err       termtext.Text
	loading   bool
	scroll    int
}

type diffMsg struct {
	w         *diffWindow
	entries   []zfs.DiffEntry
	truncated bool
	err       error
}

// openDiff opens the window, then starts reading: in that order, or a
// fast read's result arrives before there is a window to take it.
func openDiff(d Differ, snap string) tea.Cmd {
	_, open, read := diffCmds(d, snap)
	return tea.Sequence(open, read)
}

// diffCmds makes the window and the commands that open it and read.
func diffCmds(d Differ, snap string) (w *diffWindow, open, read tea.Cmd) {
	w = &diffWindow{snap: snap, loading: true}
	open = func() tea.Msg { return ui.OpenModalMsg{Modal: w} }
	read = func() tea.Msg {
		e, trunc, err := d.Diff(context.Background(), snap, "")
		return diffMsg{w: w, entries: e, truncated: trunc, err: err}
	}
	return w, open, read
}

func (w *diffWindow) Busy() bool { return false }

func (w *diffWindow) Keys() []key.Binding {
	return []key.Binding{keys.As(keys.Down, "scroll"), keys.As(keys.Up, "scroll back"), keys.As(keys.Back, "close")}
}

func (w *diffWindow) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	switch msg := msg.(type) {
	case diffMsg:
		if msg.w == w {
			w.loading, w.entries, w.truncated = false, msg.entries, msg.truncated
			if msg.err != nil {
				w.err = termtext.New(msg.err.Error())
			}
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Get(keys.Back)):
			return nil, nil
		case key.Matches(msg, keys.Get(keys.Down)):
			w.scroll++
		case key.Matches(msg, keys.Get(keys.Up)):
			w.scroll = max(w.scroll-1, 0)
		case key.Matches(msg, keys.Get(keys.First)):
			w.scroll = 0
		case key.Matches(msg, keys.Get(keys.Last)):
			w.scroll = len(w.entries)
		}
	}
	return w, nil
}

var changeWords = map[byte]struct {
	word string
	tok  theme.Token
}{
	'+': {"added", theme.OK}, '-': {"removed", theme.Error}, 'M': {"modified", theme.Warn}, 'R': {"renamed", theme.Accent},
}

var typeWords = map[byte]string{'F': "file", '/': "dir", '@': "link", 'B': "block", 'C': "char", 'P': "pipe", '=': "socket", '>': "door"}

func (w *diffWindow) Render(d ui.Display, width, height int) string {
	iw, ih := ui.Inner(width, height)
	muted := d.Palette.Style(theme.Muted)
	var lines []string
	switch {
	case w.loading:
		lines = []string{muted.Render("Reading what changed since " + termtext.New(w.snap).Display() + "…")}
	case !w.err.IsEmpty():
		lines = []string{
			d.Palette.Style(theme.Error).Render(ansi.Truncate(w.err.FirstLine().Display(), iw, "…")),
			muted.Render("Without root, this needs the delegated diff permission."),
		}
	case len(w.entries) == 0:
		lines = []string{"Nothing changed since " + termtext.New(w.snap).Display() + "."}
	default:
		lines = w.rows(d, iw, ih)
	}
	lines = append(lines, "", muted.Render("esc closes"))
	title := "changed since " + termtext.New(w.snap).Display()
	return ui.Panel{Title: ansi.Truncate(title, iw-2, "…"), Focused: true}.Render(d, strings.Join(lines, "\n"), width, min(height, len(lines)+2))
}

// rows is the summary and the visible part of the list.
func (w *diffWindow) rows(d ui.Display, iw, ih int) []string {
	counts := map[byte]int{}
	for _, e := range w.entries {
		counts[e.Change]++
	}
	sum := fmt.Sprintf("%d added, %d removed, %d modified, %d renamed", counts['+'], counts['-'], counts['M'], counts['R'])
	if w.truncated {
		sum += fmt.Sprintf("; only the first %d are shown (limits.diff_entries)", len(w.entries))
	}
	out := []string{d.Palette.Style(theme.Muted).Render(ansi.Truncate(sum, iw, "…"))}
	room := max(ih-4, 1)
	w.scroll = min(w.scroll, max(len(w.entries)-room, 0))
	for _, e := range w.entries[w.scroll:min(len(w.entries), w.scroll+room)] {
		cw := changeWords[e.Change]
		path := e.Path.Display()
		if e.Change == 'R' {
			path += " → " + e.NewPath.Display()
		}
		head := d.Palette.Style(cw.tok).Render(fmt.Sprintf("%-8s", cw.word)) + " " + fmt.Sprintf("%-4s", typeWords[e.Type]) + " "
		out = append(out, head+leftTrunc(path, max(iw-14, 8)))
	}
	return out
}

// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// fileReview is a new config file, staged in a private temporary file,
// what changes in it and what is wrong with it, awaiting w. Every tab
// of the Config view that writes a file shows one.
type fileReview struct {
	file     string
	staged   string
	data     []byte
	diff     []string
	problems []config.Problem
}

// newFileReview stages next for file, which holds cur now; userFile is
// the user's config.yaml, so next is checked with every file fstui loads.
func newFileReview(userFile, file string, cur, next []byte) (*fileReview, error) {
	staged, err := config.Stage(file, next)
	if err != nil {
		return nil, err // the message names the file
	}
	problems := config.CheckAmong(config.Files(userFile), file, next)
	return &fileReview{file: file, staged: staged, data: next, diff: config.Diff(cur, next), problems: problems}, nil
}

// key handles w and esc: done reports the review closed, and the command
// is set when the file was written, to tell the Config view.
func (r *fileReview) key(msg tea.KeyPressMsg, message *string) (cmd tea.Cmd, done bool) {
	switch {
	case key.Matches(msg, keys.Get(keys.Write)) && len(r.problems) == 0:
		if err := config.Promote(r.file, r.staged, r.data); err != nil {
			*message = err.Error()
			return nil, true
		}
		*message = "Saved " + r.file + "; the previous version is " + config.Backup(r.file) + "."
		return func() tea.Msg { return savedMsg{} }, true
	case key.Matches(msg, keys.Get(keys.Cancel)) || key.Matches(msg, keys.Get(keys.Back)):
		if err := config.Discard(r.staged); err != nil {
			*message = err.Error()
			return nil, true
		}
		*message = "Not saved; " + r.file + " is unchanged. Your edits stay until " + keyName(keys.Discard) + " or a save."
		return nil, true
	}
	return nil, false
}

// render draws the change, line by line, then extra lines (such as the
// secrets that change with it), then what w does or why it cannot.
func (r *fileReview) render(d Display, width, height int, extra []string) string {
	pal := d.Palette
	lines := []string{
		pal.Style(theme.Accent).Bold(true).Render(ansi.Wrap("Review the changes to "+r.file, width, "")),
		pal.Style(theme.Muted).Render(ansi.Wrap("temporary copy: "+r.staged, width, "")), "",
	}
	for _, l := range r.diff {
		switch {
		case strings.HasPrefix(l, "+ "):
			lines = append(lines, pal.Style(theme.OK).Render(l))
		case strings.HasPrefix(l, "- "):
			lines = append(lines, pal.Style(theme.Error).Render(l))
		default:
			lines = append(lines, pal.Style(theme.Muted).Render(l))
		}
	}
	lines = append(append(lines, extra...), "")
	write, cancel := keyName(keys.Write), keyName(keys.Cancel)
	if len(r.problems) > 0 {
		for _, p := range r.problems {
			lines = append(lines, pal.Style(theme.Error).Render(ansi.Wrap("Cannot save: "+p.String(), width, "")))
		}
		lines = append(lines, cancel+" goes back.")
	} else {
		lines = append(lines, ansi.Wrap(fmt.Sprintf("%s writes the file and keeps the current one as a backup; %s keeps it as it is.", write, cancel), width, ""))
	}
	if over := len(lines) - height; over > 0 {
		lines = append(lines[:3], lines[3+over:]...)
	}
	return strings.Join(lines, "\n")
}

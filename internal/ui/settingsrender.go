// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// settingsTop is the row of the first setting: a message line, a blank
// line and the column header come first.
const settingsTop = 3

// Render implements Screen.
func (s *Settings) Render(d Display, width, height int) string {
	if s.review != nil {
		return s.renderReview(d, width, height)
	}
	muted := d.Palette.Style(theme.Muted)
	msg := s.message
	if msg == "" {
		msg = fmt.Sprintf("Changes stay unsaved until you review them with %s; nothing is written before you confirm.", keys.Get(keys.Save).Keys()[0])
	}
	pathCol := len("SETTING")
	for _, set := range s.list {
		pathCol = max(pathCol, len(set.Path))
	}
	valueCol := len("tokyo-night") + 2
	// SOURCE is as wide as the longest source shown, at most a third of
	// the width; a long path keeps its end, the file name.
	sourceCol := len("SOURCE")
	for _, set := range s.list {
		sourceCol = max(sourceCol, len(s.sourceOf(set.Path)))
	}
	sourceCol = min(sourceCol, max(width/3, len("command line")))
	lines := []string{
		ansi.Wrap(msg, width, ""), "",
		muted.Render("  " + pad(pathCol, "SETTING") + "  " + pad(valueCol, "VALUE") + "  " + pad(sourceCol, "SOURCE") + "  DESCRIPTION"),
	}
	for i, set := range s.list {
		lines = append(lines, s.row(d, i, set, pathCol, valueCol, sourceCol))
	}
	set := s.list[s.cursor]
	help := "Values: " + strings.Join(set.Choices, ", ")
	if set.Choices == nil {
		help = fmt.Sprintf("A whole number from %d to %d; type it, then enter.", set.Min, set.Max)
	}
	lines = append(lines, "", ansi.Wrap(set.Desc+". "+help, width, ""),
		muted.Render(ansi.Wrap("keys: and colors: are edited in "+s.file+"; ? lists the keys in effect.", width, "")))
	return strings.Join(lines, "\n")
}

func pad(w int, s string) string { return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s))) }

func (s *Settings) row(d Display, i int, set config.Setting, pathCol, valueCol, sourceCol int) string {
	pal := d.Palette
	marker := "  "
	value := s.value(set.Path)
	if s.input != nil && s.input.path == set.Path {
		value = "[" + s.input.text + Cursor(d) + "]"
	}
	valueText := pad(valueCol, value)
	if _, ok := s.pending[set.Path]; ok || (s.input != nil && s.input.path == set.Path) {
		valueText = pal.Style(theme.Accent).Bold(true).Render(valueText)
	}
	name := pad(pathCol, set.Path)
	if i == s.cursor {
		marker = pal.Style(theme.Accent).Render(">") + " "
		name = lipgloss.NewStyle().Bold(true).Render(name)
	}
	src := ansi.TruncateLeft(s.sourceOf(set.Path), max(len(s.sourceOf(set.Path))-sourceCol, 0), "…")
	return marker + name + "  " + valueText + "  " + pal.Style(theme.Muted).Render(pad(sourceCol, src)+"  "+set.Desc)
}

func (s *Settings) renderReview(d Display, width, height int) string {
	pal := d.Palette
	r := s.review
	title := "Review the changes to " + s.file
	if r.revert {
		title = "Revert " + s.file + " to its previous version"
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(ansi.Wrap(title, width, "")),
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
	lines = append(lines, "")
	confirm, cancel := keys.Get(keys.Write).Keys()[0], keys.Get(keys.Cancel).Keys()[0]
	if len(r.problems) > 0 {
		for _, p := range r.problems {
			lines = append(lines, pal.Style(theme.Error).Render(ansi.Wrap("Cannot save: "+p.String(), width, "")))
		}
		lines = append(lines, cancel+" goes back.")
	} else {
		lines = append(lines, ansi.Wrap(fmt.Sprintf("%s writes the file and keeps the current one as %s; %s keeps the current file.",
			confirm, config.Backup(s.file), cancel), width, ""))
	}
	// Long diffs keep their end, where the decision is.
	if extra := len(lines) - height; extra > 0 {
		lines = append(lines[:3], lines[3+extra:]...)
	}
	return strings.Join(lines, "\n")
}

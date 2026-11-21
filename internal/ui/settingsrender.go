// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// settingsTop is the row of the first group or setting: the message
// line, a blank line, the heading and a blank line come first.
const settingsTop = 4

// Render implements Screen: the list of groups, or the open group's
// settings with the selected one explained below them.
func (s *Settings) Render(d Display, width, height int) string {
	return styleKeys(d, s.render(d, width, height))
}

func (s *Settings) render(d Display, width, height int) string {
	if s.review != nil {
		return s.renderReview(d, width, height)
	}
	msg := s.message
	if msg == "" {
		msg = fmt.Sprintf("Nothing is written until you review your changes with %s and confirm.", keyName(keys.Save))
	}
	head := []string{ansi.Truncate(msg, width, "…"), ""}
	if s.section < 0 {
		return strings.Join(append(head, s.groups(d, width)...), "\n")
	}
	return strings.Join(append(head, s.groupSettings(d, width, height-len(head))...), "\n")
}

// groups lists the groups of settings, with what each covers and how
// many of its settings are changed and not saved.
func (s *Settings) groups(d Display, width int) []string {
	pal := d.Palette
	titleCol := 0
	for _, g := range config.Sections {
		titleCol = max(titleCol, len(g.Title))
	}
	out := []string{pal.Style(theme.Muted).Render("Settings by group; " + keyName(keys.Select) + " opens one."), ""}
	for i, g := range config.Sections {
		marker, title := "  ", pad(titleCol, g.Title)
		if i == s.group {
			marker = pal.Style(theme.Accent).Render(">") + " "
			title = lipgloss.NewStyle().Bold(true).Render(title)
		}
		line := marker + title + "    " + pal.Style(theme.Muted).Render(g.Desc)
		if n := s.unsavedIn(i); n > 0 {
			line += "  " + pal.Style(theme.Accent).Render(fmt.Sprintf("%d unsaved", n))
		}
		out = append(out, ansi.Truncate(line, width, "…"))
	}
	return out
}

func (s *Settings) unsavedIn(g int) int {
	n := 0
	for p := range s.pending {
		if config.SectionOf(p) == g {
			n++
		}
	}
	return n
}

// groupSettings is the open group: its settings as label and value, as
// many as fit above the explanation of the selected one, scrolled to keep
// it in view.
func (s *Settings) groupSettings(d Display, width, height int) []string {
	pal := d.Palette
	g := config.Sections[s.section]
	it := s.items()
	labelCol := 0
	for _, i := range it {
		labelCol = max(labelCol, lipgloss.Width(s.list[i].Label()))
	}
	labelCol = min(labelCol, width/2)
	detail := Box(d, s.list[s.cursor].Label(), s.detail(d, width-3), width)
	rows := max(height-4-len(detail), 3) // the title, a blank line, "more below" and a blank line
	at := slices.Index(it, s.cursor)
	s.top = min(max(s.top, at-rows+1), at, max(len(it)-rows, 0))
	s.top = max(s.top, 0)
	title := lipgloss.NewStyle().Bold(true).Render(g.Title) + pal.Style(theme.Muted).Render("  "+keyName(keys.Back)+" returns to the groups")
	if s.top > 0 {
		title += pal.Style(theme.Muted).Render(fmt.Sprintf("; %d more above", s.top))
	}
	out := []string{title, ""}
	for k := s.top; k < len(it) && k < s.top+rows; k++ {
		out = append(out, s.row(d, it[k], labelCol))
	}
	if more := len(it) - s.top - rows; more > 0 {
		out = append(out, pal.Style(theme.Muted).Render(fmt.Sprintf("  %d more below", more)))
	}
	return append(append(out, ""), detail...)
}

// row is one setting: its label and its value; a value still at its
// default is muted, an unsaved one stands out.
func (s *Settings) row(d Display, i, labelCol int) string {
	pal := d.Palette
	set := s.list[i]
	marker := "  "
	label := pad(labelCol, ansi.Truncate(set.Label(), labelCol, "…"))
	if i == s.cursor {
		marker = pal.Style(theme.Accent).Render(">") + " "
		label = lipgloss.NewStyle().Bold(true).Render(label)
	}
	value := s.value(set.Path)
	switch _, unsaved := s.pending[set.Path]; {
	case s.input != nil && s.input.path == set.Path:
		value = pal.Style(theme.Accent).Bold(true).Render("[" + s.input.text + Cursor(d) + "]")
	case unsaved:
		value = pal.Style(theme.Accent).Bold(true).Render(value) + pal.Style(theme.Accent).Render("  unsaved")
	case value == "":
		value = pal.Style(theme.Muted).Render("not set")
	case s.sourceOf(set.Path) == "default":
		value = pal.Style(theme.Muted).Render(value)
	}
	return marker + label + "    " + value
}

// detail explains the selected setting: what it does, what it takes, its
// key in the file and where its value comes from.
func (s *Settings) detail(d Display, width int) []string {
	muted := d.Palette.Style(theme.Muted)
	set := s.list[s.cursor]
	help := set.Help() + "."
	if set.Typed() {
		help = set.Help() + "; " + keyName(keys.Select) + " to type it."
	} else {
		help += " " + keyName(keys.Select) + " steps through them."
	}
	if set.Restart {
		help += " Applies after a restart."
	}
	out := []string{ansi.Wrap(set.Desc+".", width, ""), ansi.Wrap(help, width, "")}
	out = append(out, muted.Render(ansi.Wrap(set.Path+" in the file; now from "+s.sourceOf(set.Path)+".", width, "")))
	if s.file != "" && s.section == 0 {
		out = append(out, muted.Render(ansi.Wrap("Keys and colours are edited in "+s.file+" or its config.d (keys:, colors:).", width, "")))
	}
	return out
}

func pad(w int, s string) string { return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s))) }

func (s *Settings) renderReview(d Display, width, height int) string {
	pal := d.Palette
	r := s.review
	title := "Review the changes"
	if r.revert {
		title = "Revert to the previous versions"
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Render(title)}
	for _, f := range r.files {
		lines = append(lines, "", lipgloss.NewStyle().Bold(true).Render(ansi.Wrap(f.file, width, "")),
			pal.Style(theme.Muted).Render(ansi.Wrap("temporary copy: "+f.staged, width, "")))
		for _, l := range f.diff {
			switch {
			case strings.HasPrefix(l, "+ "):
				lines = append(lines, pal.Style(theme.OK).Render(l))
			case strings.HasPrefix(l, "- "):
				lines = append(lines, pal.Style(theme.Error).Render(l))
			default:
				lines = append(lines, pal.Style(theme.Muted).Render(l))
			}
		}
	}
	lines = append(lines, "")
	confirm, cancel := keyName(keys.Write), keyName(keys.Cancel)
	if len(r.problems) > 0 {
		for _, p := range r.problems {
			lines = append(lines, pal.Style(theme.Error).Render(ansi.Wrap("Cannot save: "+p.String(), width, "")))
		}
		lines = append(lines, cancel+" goes back.")
	} else {
		lines = append(lines, ansi.Wrap(fmt.Sprintf("%s writes the files and keeps the current ones as .bak; %s keeps them as they are.", confirm, cancel), width, ""))
	}
	// Long diffs keep their end, where the decision is.
	if extra := len(lines) - height; extra > 0 {
		lines = append(lines[:1], lines[1+extra:]...)
	}
	return strings.Join(lines, "\n")
}

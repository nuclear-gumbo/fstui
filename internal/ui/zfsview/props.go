// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// minHelpHeight: below this the help block would leave too few rows for
// the list itself, so it is left out.
const minHelpHeight = 14

// maxSourceShare: the source column ("inherited from tank/a/b") takes at
// most this fraction of the width; values get the rest.
const maxSourceShare = 3

// maxHelpShare: the help block takes at most this fraction of the view;
// longer help is cut at its last line.
const maxHelpShare = 3

// propTable describes one property list: which properties to show, how
// to label them, which are byte counts, and where their help comes from.
type propTable struct {
	props  zfs.Properties
	filter func(name string) bool
	first  string // the first column's title: PROPERTY or FEATURE
	header string // the second column's title: VALUE or STATE
	bytes  map[string]bool
	help   func(name string) (zfs.Help, bool)
}

// render draws the tab bar, the list with the row at *sel selected and
// kept in view, and that property's help below. j and k move *sel.
func (t propTable) render(disp ui.Display, sel *int, width, height int) string {
	muted := disp.Palette.Style(theme.Muted)
	var names []string
	nameCol := len(t.first)
	for n := range t.props {
		if t.filter(n) {
			names = append(names, n)
			nameCol = max(nameCol, len(strings.TrimPrefix(n, "feature@")))
		}
	}
	sort.Strings(names)
	*sel = max(min(*sel, len(names)-1), 0)
	var help []string
	if height >= minHelpHeight && len(names) > 0 {
		help = t.helpBlock(disp, names[*sel], width)
		help = help[:min(len(help), height/maxHelpShare)]
	}
	rows := max(height-1-len(help), 1) // below the header
	start := max(0, *sel-rows+1)
	lines := []string{muted.Render("  " + pad(t.first, nameCol) + sp + t.header)}
	srcCol := 0
	for _, n := range names {
		srcCol = max(srcCol, lipgloss.Width(sourceText(t.props[n].Source)))
	}
	srcCol = min(srcCol, width/maxSourceShare)
	for i := start; i < len(names) && i < start+rows; i++ {
		row := propertyRow(disp, names[i], t.props[names[i]], nameCol, srcCol, width-markerWidth, t.bytes)
		marker := "  "
		if i == *sel {
			marker = disp.Palette.Style(theme.Accent).Render(">") + " "
			row = lipgloss.NewStyle().Bold(true).Render(row)
		}
		lines = append(lines, marker+row)
	}
	if len(help) > 0 {
		for len(lines) < height-len(help) {
			lines = append(lines, "")
		}
		lines = append(lines, help...)
	}
	return strings.Join(lines, "\n")
}

// helpBlock is a blank line, then the help wrapped to width: the name and
// summary, the valid values, and the effect on existing data.
func (t propTable) helpBlock(disp ui.Display, name string, width int) []string {
	muted := disp.Palette.Style(theme.Muted)
	label := lipgloss.NewStyle().Bold(true).Render(name) + "  "
	var parts []string
	switch h, ok := t.help(name); {
	case ok:
		parts = append(parts, label+h.Summary)
		if h.Values != "" {
			parts = append(parts, muted.Render("Values: ")+h.Values)
		}
		if e := h.Effect.Text(); e != "" {
			parts = append(parts, muted.Render(e))
		}
	case strings.Contains(name, ":"):
		parts = append(parts, label+"User property: set by an administrator or a tool, not interpreted by ZFS.")
	default:
		parts = append(parts, label+"No description: zfsprops(7) does not document this property.")
	}
	out := []string{""}
	for _, p := range parts {
		out = append(out, strings.Split(ansi.Wrap(p, width, ""), "\n")...)
	}
	return out
}

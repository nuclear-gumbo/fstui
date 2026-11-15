// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"maps"
	"slices"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// marks are the items marked with m for one change to several at once. They
// are names, so they survive refreshes; a name that disappears is dropped when
// the list is read again.
type marks map[string]bool

func (m marks) toggle(name string) {
	if m[name] {
		delete(m, name)
		return
	}
	m[name] = true
}

// sorted returns the marked names in order.
func (m marks) sorted() []string { return slices.Sorted(maps.Keys(m)) }

// keep drops marks on names no longer listed.
func (m marks) keep(listed func(string) bool) {
	for n := range m {
		if !listed(n) {
			delete(m, n)
		}
	}
}

// rowMarker is the two cells before a row: ">" for the cursor, "*" for
// a mark.
func rowMarker(d ui.Display, selected, marked bool) string {
	cur, mark := " ", " "
	if selected {
		cur = d.Palette.Style(theme.Accent).Render(">")
	}
	if marked {
		mark = d.Palette.Style(theme.Accent).Render("*")
	}
	return cur + mark
}

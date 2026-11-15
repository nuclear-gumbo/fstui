// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// listCursor is the selection in a flat list: j/k, g/G. first is the row drawn
// at the top by the last render, so a click can be mapped to a row.
type listCursor struct{ i, first int }

func listKeys() []key.Binding {
	return []key.Binding{keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"), keys.Get(keys.First), keys.Get(keys.Last)}
}

// move handles a navigation key for a list of n rows and reports whether
// it was one.
func (c *listCursor) move(msg tea.KeyPressMsg, n int) bool {
	switch {
	case key.Matches(msg, keys.As(keys.Down, "next")):
		c.i++
	case key.Matches(msg, keys.As(keys.Up, "previous")):
		c.i--
	case key.Matches(msg, keys.Get(keys.First)):
		c.i = 0
	case key.Matches(msg, keys.Get(keys.Last)):
		c.i = n - 1
	default:
		return false
	}
	c.clamp(n)
	return true
}

func (c *listCursor) clamp(n int) { c.i = max(min(c.i, n-1), 0) }

// window returns the rows to draw so the cursor stays visible.
func (c *listCursor) window(n, rows int) (start, end int) {
	start = max(0, c.i-rows+1)
	c.first = start
	return start, min(start+rows, n)
}

// mouse moves the selection for the wheel, or to a clicked row; top is
// the screen row of the list's first row. It reports whether msg was a
// mouse message.
func (c *listCursor) mouse(msg tea.Msg, n, top int) bool {
	switch msg := msg.(type) {
	case ui.ScrollMsg:
		c.i += msg.Delta
	case ui.ClickMsg:
		if row := c.first + msg.Y - top; msg.Y >= top && row < n {
			c.i = row
		}
	default:
		return false
	}
	c.clamp(n)
	return true
}

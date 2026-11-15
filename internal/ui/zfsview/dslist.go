// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// Datasets list columns.
const (
	ratioWidth = len("RATIO") + 1
	encWidth   = len("locked")
	// treeMarker is the expand marker and its space ("▸ ").
	treeMarker = 2
)

// dsColumns says which optional columns of the dataset tree fit. The
// mountpoint is in the detail's summary.
type dsColumns struct {
	name                     int
	avail, refer, ratio, enc bool
}

// dsLayout fits columns in priority order after name and used: available,
// referenced, encryption, ratio.
func dsLayout(rows []*node, width int) dsColumns {
	need := len("NAME")
	for _, n := range rows {
		need = max(need, n.depth*indent+treeMarker+len(n.d.Name[strings.LastIndexByte(n.d.Name, '/')+1:]))
	}
	c := dsColumns{}
	spare := width - markerWidth - gap - sizeWidth - min(need, width/2)
	take := func(on *bool, w int) {
		if spare >= w {
			*on, spare = true, spare-w
		}
	}
	take(&c.avail, gap+sizeWidth)
	take(&c.refer, gap+sizeWidth)
	take(&c.enc, gap+encWidth)
	take(&c.ratio, gap+ratioWidth)
	used := markerWidth + gap + sizeWidth + widthIf(c.avail, gap+sizeWidth) + widthIf(c.refer, gap+sizeWidth) +
		widthIf(c.enc, gap+encWidth) + widthIf(c.ratio, gap+ratioWidth)
	c.name = max(width-used, len("NAME"))
	return c
}

// widthIf is w for a column that is shown, else 0.
func widthIf(on bool, w int) int {
	if on {
		return w
	}
	return 0
}

func (v *Datasets) renderList(disp ui.Display, width, height int) string {
	muted := disp.Palette.Style(theme.Muted)
	switch {
	case v.poll.loaded.IsZero() && v.poll.lastErr.IsEmpty():
		return muted.Render("Reading datasets")
	case v.poll.loaded.IsZero():
		return "Could not read datasets\n" + v.poll.lastErr.Display() + "\n" + muted.Render("fstui retries automatically; r retries now.")
	case len(v.poll.data) == 0:
		return "No datasets: no pools are imported."
	}
	rows := v.rows()
	cur := v.cursor(rows)
	c := dsLayout(rows, width)
	head := strings.Repeat(" ", markerWidth) + pad("NAME", c.name) + sp + lpad("USED", sizeWidth)
	for _, col := range []struct {
		on    bool
		title string
		w     int
	}{{c.avail, "AVAIL", sizeWidth}, {c.refer, "REFER", sizeWidth}, {c.ratio, "RATIO", ratioWidth}} {
		if col.on {
			head += sp + lpad(col.title, col.w)
		}
	}
	if c.enc {
		head += sp + "ENC"
	}
	lines := []string{muted.Render(head)}
	body := max(height-1, 1)
	start := max(0, cur-body+1)
	v.first = start
	for i := start; i < len(rows) && i < start+body; i++ {
		lines = append(lines, datasetRow(disp, rows[i], i == cur, v.marked[rows[i].d.Name], v.collapsed[rows[i].d.Name], c))
	}
	return strings.Join(lines, "\n")
}

func datasetRow(disp ui.Display, n *node, selected, marked, collapsed bool, c dsColumns) string {
	d := n.d
	marker := rowMarker(disp, selected, marked)
	label := treeLabel(disp, n, collapsed, c.name)
	if selected {
		label = lipgloss.NewStyle().Bold(true).Render(label)
	}
	size := func(prop string) string {
		v, _ := d.Properties.Number(prop)
		return lpad(ui.Size(v, disp.Exact), sizeWidth)
	}
	row := marker + pad(label, c.name) + sp + size("used")
	if c.avail {
		row += sp + size("available")
	}
	if c.refer {
		row += sp + size("referenced")
	}
	if c.ratio {
		row += sp + lpad(d.Properties["compressratio"].Text.Display()+"x", ratioWidth)
	}
	if c.enc {
		row += sp + encryption(disp, d)
	}
	return row
}

// treeLabel is a row's name: roots in full, children by their last
// component, indented under their parent with an expand marker.
func treeLabel(disp ui.Display, n *node, collapsed bool, width int) string {
	label := n.d.Name
	if i := strings.LastIndexByte(label, '/'); i >= 0 && n.depth > 0 {
		label = label[i+1:]
	}
	open, closed := "▾ ", "▸ "
	if disp.ASCII {
		open, closed = "v ", "> "
	}
	switch {
	case len(n.children) == 0:
		label = "  " + label
	case collapsed:
		label = closed + label
	default:
		label = open + label
	}
	return leftTrunc(strings.Repeat(" ", n.depth*indent)+label, width)
}

// encryption is blank for unencrypted datasets, "on" when the key is
// loaded, and "locked" when it is not.
func encryption(disp ui.Display, d zfs.Dataset) string {
	if d.Properties["encryption"].Text.Raw() == "off" {
		return ""
	}
	if d.Properties["keystatus"].Text.Raw() == "unavailable" {
		return disp.Palette.Style(theme.Warn).Render("locked")
	}
	return "on"
}

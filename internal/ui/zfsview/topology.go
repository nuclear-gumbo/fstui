// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

// The Topology tab of pool detail: zpool status as text, the checkpoint
// and scan lines, and the device tree.

import (
	"fmt"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// topology draws zpool status: explanation, checkpoint, scan, device
// tree. While the detail has the focus, the selected device is
// highlighted and its line returned; otherwise the line is -1.
func (p *Pools) topology(d ui.Display, o zfs.Overview, width int) ([]string, int) {
	pal := d.Palette
	st := o.Status
	var lines []string
	for _, l := range st.Status {
		lines = append(lines, pal.Style(theme.Warn).Render(l.Display()))
	}
	for _, l := range st.Action {
		lines = append(lines, pal.Style(theme.Muted).Render(l.Display()))
	}
	if line := p.scheduleLine(d, o); line != "" {
		lines = append(lines, line)
	}
	if c := st.Checkpoint; c != nil {
		lines = append(lines, checkpointLine(*c, d.Exact))
	}
	if st.Expansion != nil {
		lines = append(lines, expandLine(st, p.opts.Now(), d.Exact))
	}
	if st.Removal != nil {
		lines = append(lines, removeLine(st, p.opts.Now(), d.Exact))
	}
	if sc := st.Scan; sc != nil {
		lines = append(lines, scanLine(*sc, p.opts.Now(), d.Exact))
	}
	// Explanations wrap to the panel instead of running off its edge.
	var wrapped []string
	for _, l := range lines {
		wrapped = append(wrapped, ui.WrapStyled(l, width)...)
	}
	if len(wrapped) > 0 {
		wrapped = append(wrapped, "")
	}
	sel := ""
	if p.focus {
		if leaves := leafNames(st); len(leaves) > 0 {
			p.detail.device = min(p.detail.device, len(leaves)-1)
			sel = leaves[p.detail.device]
		}
	}
	tree, cur := deviceTree(d, st, width, sel)
	if cur >= 0 {
		cur += len(wrapped)
	}
	return append(wrapped, tree...), cur
}

// leafNames lists the pool's devices that can be changed (the leaves of
// the device tree), in the order the tree shows them.
func leafNames(st zfs.PoolStatus) []string {
	var out []string
	st.Walk(func(v zfs.Vdev) {
		if len(v.Children) == 0 && v.Type != "root" {
			out = append(out, v.Name.Raw())
		}
	})
	return out
}

func checkpointLine(c zfs.Checkpoint, exact bool) string {
	if c.State == "DISCARDING" {
		return "Checkpoint being discarded."
	}
	return fmt.Sprintf("Checkpoint taken %s, holds %s. Remove, attach, detach, split and reguid are blocked until it is discarded.",
		c.Taken.Local().Format("2006-01-02 15:04"), format.Size(c.Space, exact))
}

// expandLine summarises a RAIDZ expansion as zpool status does.
func expandLine(st zfs.PoolStatus, now time.Time, exact bool) string {
	e, vdev := *st.Expansion, termtext.New(st.ExpandingName()).Display()
	if e.State != "SCANNING" {
		return fmt.Sprintf("Expanded %s %s ago, copying %s in %s.", vdev, format.Age(now.Sub(e.End)), format.Size(e.Reflowed, exact), format.Age(e.End.Sub(e.Start)))
	}
	s := fmt.Sprintf("Expanding %s: %s of %s copied, started %s ago", vdev, format.Size(e.Reflowed, exact), format.Size(e.ToReflow, exact), format.Age(now.Sub(e.Start)))
	if elapsed := now.Sub(e.Start); e.Reflowed > 0 && e.Reflowed < e.ToReflow && elapsed > 0 {
		s += ", about " + format.Age(time.Duration(float64(elapsed)*float64(e.ToReflow-e.Reflowed)/float64(e.Reflowed))) + " left"
	}
	if e.Resilver {
		s += "; waiting for a resilver"
	}
	return s + ". A scrub follows it."
}

// removeLine summarises a vdev removal as zpool status does.
func removeLine(st zfs.PoolStatus, now time.Time, exact bool) string {
	r, vdev := *st.Removal, termtext.New(st.RemovingName()).Display()
	switch r.State {
	case "SCANNING":
		s := fmt.Sprintf("Removing %s: %s of %s copied, started %s ago", vdev, format.Size(r.Copied, exact), format.Size(r.ToCopy, exact), format.Age(now.Sub(r.Start)))
		if elapsed := now.Sub(r.Start); r.Copied > 0 && r.Copied < r.ToCopy && elapsed > 0 {
			s += ", about " + format.Age(time.Duration(float64(elapsed)*float64(r.ToCopy-r.Copied)/float64(r.Copied))) + " left"
		}
		return s + "."
	case "CANCELED":
		return fmt.Sprintf("Removal of %s cancelled %s ago; it stays in the pool.", vdev, format.Age(now.Sub(r.End)))
	}
	return fmt.Sprintf("Removed %s %s ago, copying %s; its table of moved blocks uses %s of memory.", vdev, format.Age(now.Sub(r.End)), format.Size(r.Copied, exact), format.Size(r.Mapping, exact))
}

// scanLine summarises a scrub or resilver as zpool status does. Progress
// can pass 100% (zpool-scrub(8)); then no estimate is given.
func scanLine(sc zfs.Scan, now time.Time, exact bool) string {
	word := "Scan"
	if f := sc.Function; f != "" {
		word = strings.ToUpper(f[:1]) + strings.ToLower(f[1:])
	}
	switch {
	case sc.State == "SCANNING" && sc.Paused:
		return fmt.Sprintf("%s paused %s ago at %s of %s.", word, format.Age(now.Sub(sc.PausedAt)), format.Size(sc.Issued, exact), format.Size(sc.ToExamine, exact))
	case sc.State == "SCANNING":
		elapsed := now.Sub(sc.Start)
		s := fmt.Sprintf("%s running: %s of %s issued, started %s ago", word, format.Size(sc.Issued, exact), format.Size(sc.ToExamine, exact), format.Age(elapsed))
		if sc.Issued > 0 && sc.Issued < sc.ToExamine && elapsed > 0 {
			left := time.Duration(float64(elapsed) * float64(sc.ToExamine-sc.Issued) / float64(sc.Issued))
			s += ", about " + format.Age(left) + " left"
		}
		return s + "."
	case sc.State == "CANCELED":
		return fmt.Sprintf("%s cancelled %s ago.", word, format.Age(now.Sub(sc.End)))
	}
	return fmt.Sprintf("%s finished %s ago in %s: repaired %s, %d errors.",
		word, format.Age(now.Sub(sc.End)), format.Age(sc.End.Sub(sc.Start)), format.Size(sc.Processed, exact), sc.Errors)
}

// Device tree columns, as zpool status prints them.
const (
	stateCol = len("DEGRADED")
	countCol = len("CKSUM") + 1
	indent   = 2
)

func deviceTree(d ui.Display, st zfs.PoolStatus, width int, sel string) ([]string, int) {
	// The name column fits the longest name, so the counts sit next to
	// the names instead of at the far edge of a wide terminal.
	nameCol := len("NAME")
	var measure func(v zfs.Vdev, depth int)
	measure = func(v zfs.Vdev, depth int) {
		nameCol = max(nameCol, depth*indent+lipgloss.Width(v.Name.Display()))
		for _, c := range v.Children {
			measure(c, depth+1)
		}
	}
	measure(st.Data, 0)
	for _, g := range [][]zfs.Vdev{st.Special, st.Dedup, st.Logs, st.Cache, st.Spares} {
		for _, v := range g {
			measure(v, 1)
		}
	}
	nameCol = min(nameCol, max(width-stateCol-3*countCol-gap, len("NAME")))
	lines := []string{d.Palette.Style(theme.Muted).Render(
		pad("NAME", nameCol) + sp + pad("STATE", stateCol) + lpad("READ", countCol) + lpad("WRITE", countCol) + lpad("CKSUM", countCol),
	)}
	cur := -1
	var walk func(v zfs.Vdev, depth int)
	walk = func(v zfs.Vdev, depth int) {
		row := deviceRow(d, v, depth, nameCol)
		if sel != "" && cur < 0 && len(v.Children) == 0 && v.Name.Raw() == sel {
			cur = len(lines)
			row = d.Palette.Style(theme.Accent).Render(">") + ansi.Cut(row, 1, ansi.StringWidth(row))
			row = lipgloss.NewStyle().Bold(true).Render(row)
		}
		lines = append(lines, row)
		for _, c := range v.Children {
			walk(c, depth+1)
		}
	}
	walk(st.Data, 0)
	for _, g := range []struct {
		label string
		vdevs []zfs.Vdev
	}{{"special", st.Special}, {"dedup", st.Dedup}, {"logs", st.Logs}, {"cache", st.Cache}, {"spares", st.Spares}} {
		if len(g.vdevs) == 0 {
			continue
		}
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render(g.label))
		for _, v := range g.vdevs {
			walk(v, 1)
		}
	}
	return lines, cur
}

func deviceRow(d ui.Display, v zfs.Vdev, depth, nameCol int) string {
	pal := d.Palette
	nm := strings.Repeat(" ", depth*indent) + leftTrunc(v.Name.Display(), nameCol-depth*indent)
	row := pad(nm, nameCol) + sp + pal.Style(stateToken(v.State)).Render(pad(v.State, stateCol))
	for _, n := range []uint64{v.ReadErrors, v.WriteErrors, v.ChecksumErrors} {
		tok := theme.Muted
		if n > 0 {
			tok = theme.Error
		}
		row += pal.Style(tok).Render(lpad(fmt.Sprint(n), countCol))
	}
	return row
}

// leftTrunc keeps the end of a device path, which is the part that
// differs between devices.
func leftTrunc(s string, w int) string {
	if ansi.StringWidth(s) <= w || w < 1 {
		return s
	}
	return "…" + ansi.TruncateLeft(s, ansi.StringWidth(s)-w+1, "")
}

// scheduleLine says what scrubs the pool and when, under its status.
func (p *Pools) scheduleLine(d ui.Display, o zfs.Overview) string {
	muted := d.Palette.Style(theme.Muted)
	s, scheduled, known := p.schedule(o.Pool.Name.String())
	switch {
	case !known:
		return ""
	case !scheduled:
		return muted.Render("No scrub is scheduled: no OpenZFS scrub timer or Debian cron job covers this pool.")
	case s.Next.IsZero():
		return muted.Render("Scrubbed by " + s.By + ".")
	}
	return muted.Render("Scrubbed by "+s.By+", next ") + s.Next.In(p.opts.Now().Location()).Format("2006-01-02 15:04") + "."
}

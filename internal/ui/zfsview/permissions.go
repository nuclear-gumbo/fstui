// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// permissionLines is the Permissions tab of dataset detail: who may do
// what on ds without root, from zfs allow on ds and its ancestors. A
// grant on ds applies here, below, or both, as it was made; an
// ancestor's grant reaches ds only if it was made for descendents
// (zfs-allow(8)).
func permissionLines(disp ui.Display, ds string, delegs []zfs.Delegation, width int) []string {
	muted := disp.Palette.Style(theme.Muted)
	if len(delegs) == 0 {
		return []string{
			"Nothing is delegated on this dataset or above it (zfs allow).",
			muted.Render("Only root can change it."),
		}
	}
	const whoCol = 24
	permCol := max((width-whoCol-2*gap)/2, len("PERMISSIONS"))
	row := func(who, perms, applies string) string {
		return pad(ansi.Truncate(who, whoCol, "…"), whoCol) + sp + pad(ansi.Truncate(perms, permCol, "…"), permCol) + sp + muted.Render(applies)
	}
	lines := []string{muted.Render(row("WHO", "PERMISSIONS", "APPLIES"))}
	var sets, create []string
	for _, d := range delegs {
		on := d.Dataset.Display()
		own := d.Dataset.Raw() == ds
		for _, g := range d.Grants {
			if applies := grantApplies(g.Scope, own, on); applies != "" {
				lines = append(lines, row(grantee(g), strings.Join(g.Perms, ","), applies))
			}
		}
		for _, s := range d.Sets {
			sets = append(sets, row(s.Name, strings.Join(s.Perms, ","), "set defined on "+on))
		}
		if len(d.CreateTime) > 0 {
			create = append(create, row("dataset creator", strings.Join(d.CreateTime, ","), "on what they create below "+on))
		}
	}
	if len(lines) == 1 {
		lines = append(lines, muted.Render("No one has delegated permissions on this dataset."))
	}
	if len(create) > 0 {
		lines = append(append(lines, ""), create...)
	}
	if len(sets) > 0 {
		lines = append(append(lines, "", muted.Render("Permission sets")), sets...)
	}
	return lines
}

// grantApplies says where a grant set on dataset on takes effect, seen
// from the dataset shown; empty if it does not reach it.
func grantApplies(scope zfs.GrantScope, own bool, on string) string {
	switch {
	case own && scope == zfs.Local:
		return "here"
	case own && scope == zfs.Descendent:
		return "on datasets below only"
	case own && scope == zfs.LocalAndDescendent:
		return "here and below"
	case scope == zfs.Descendent || scope == zfs.LocalAndDescendent:
		return "here, from " + on
	}
	return ""
}

func grantee(g zfs.Grant) string {
	if g.Who == "everyone" {
		return "everyone"
	}
	return g.Who + " " + g.Name.Display()
}

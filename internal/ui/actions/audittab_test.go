// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/audit"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// Each title sits over its column, and a step's result reads success,
// failed or n/a.
func TestAuditTable(t *testing.T) {
	zero, two := 0, 2
	a := NewAudit(&Session{paths: Paths{AuditDir: "/var/log/fstui"}})
	a.Update(auditMsg{recs: []audit.Record{
		{Time: "2026-11-19T05:02:29Z", Mode: "write", Phase: "result", Op: "snapshot-destroy", ExitCode: &zero, Argv: []string{"zfs", "destroy", "--", "data/games@a"}},
		{Time: "2026-11-19T05:02:28Z", Mode: "write", Phase: "attempt", Op: "snapshot-destroy", Argv: []string{"zfs", "destroy", "--", "data/games@a"}},
		{Time: "2026-11-15T02:22:06Z", Mode: "write", Phase: "result", Op: "pool-scrub", ExitCode: &two, Argv: []string{"zpool", "scrub", "--", "tank"}},
	}})
	out := ansi.Strip(a.Render(ui.Display{Palette: theme.Resolve(theme.Theme{}, true, colorprofile.ASCII, nil)}, 110, 24))
	t.Log("\n" + out)
	lines := strings.Split(out, "\n")
	head := lines[2]
	for _, row := range lines[4:7] {
		for _, col := range []string{"MODE", "PHASE", "CHANGE", "RESULT", "COMMAND"} {
			at := strings.Index(head, col)
			if at < 1 || []rune(row)[len([]rune(head[:at]))-2] != '│' {
				t.Errorf("%s does not start a column in\n%s\n%s", col, head, row)
			}
		}
	}
	for i, want := range []string{"success", "n/a", "failed"} {
		if !strings.Contains(lines[4+i], " "+want+" ") {
			t.Errorf("row %d lacks %q: %s", i, want, lines[4+i])
		}
	}
}

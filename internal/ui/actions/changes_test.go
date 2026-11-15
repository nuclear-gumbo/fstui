// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/audit"
	"github.com/nuclear-gumbo/fstui/internal/execute"
)

func TestAuditTabReadsAndFilters(t *testing.T) {
	s := savedSession(t, nil)
	l, err := audit.Open(s.paths.AuditDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"snapshot-create", "dataset-destroy"} {
		ev := execute.StepEvent{
			Mode: execute.Write, Plan: execute.PlanRef{Op: op}, Step: 1, Phase: execute.Attempt,
			Argv: execute.Argv{Tool: execute.ZFS, Options: []string{"x"}},
		}
		if err := l.Record(ev); err != nil {
			t.Fatal(err)
		}
	}
	_ = l.Close()
	v := NewChanges(s)
	v.Update(tea.KeyPressMsg{Code: 'l', Text: "l"}) // to the audit tab
	if v.tab != tabAudit {
		t.Fatalf("tab = %d", v.tab)
	}
	v.Update(v.audit.load()())
	if len(v.audit.shown()) != 2 || v.audit.shown()[0].Op != "dataset-destroy" {
		t.Fatalf("records = %+v", v.audit.recs)
	}
	for _, k := range []string{"/", "d", "e", "s", "t"} {
		v.Update(tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
	}
	if !v.Typing() || len(v.audit.shown()) != 1 {
		t.Fatalf("typing %v, filtered %d", v.Typing(), len(v.audit.shown()))
	}
	v.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if v.Typing() || len(v.audit.shown()) != 2 {
		t.Fatalf("after esc: typing %v, %d records", v.Typing(), len(v.audit.shown()))
	}
}

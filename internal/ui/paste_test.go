// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// A password pasted from a password manager lands in the secret field
// whole, hidden, without the trailing newline finishing the form.
func TestPasteIntoSecret(t *testing.T) {
	n, _ := editTab(t)
	cfgView := NewConfig(NewSettings(n.file, nil, n.cfg, nil, config.Load, nil), n, NewAlerts(n.file, n.cfg, nil), NewSchedules(nil, "", time.Minute, time.Now))
	cfgView.tab = tabNotifications
	m := sized(newModel(colorprofile.ASCII, cfgView), 120, 40)
	m = step(t, m, k("a"))
	if n.form == nil {
		t.Fatal("no form")
	}
	for i, f := range n.form.fields() {
		if f.secret {
			n.form.cursor = i
		}
	}
	const pw = "Xk9#mQ2$vL7!pR4&wZ8*nB3^tY6%hJ1@"
	m = step(t, m, tea.PasteMsg{Content: pw + "\n"})
	if n.form == nil || n.form.secret != pw {
		t.Fatalf("secret %q, form open %v", n.form.secret, n.form != nil)
	}
	if view := ansi.Strip(m.View().Content); strings.Contains(view, pw) || !strings.Contains(view, strings.Repeat("*", len(pw))) {
		t.Error("the pasted secret is not hidden")
	}

	// A paste where nothing takes text is dropped, not run as keys.
	n.form = nil
	step(t, m, tea.PasteMsg{Content: "ad"})
	if n.form != nil || n.message != "" {
		t.Errorf("a paste outside a field acted: form %v, message %q", n.form != nil, n.message)
	}
}

func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	out, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return out
}

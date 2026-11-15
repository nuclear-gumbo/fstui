// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// Message is a modal that only says something; esc or enter closes it.
type Message struct{ title, text string }

// NewMessage returns a message modal.
func NewMessage(title, text string) *Message { return &Message{title: title, text: text} }

// Busy implements ui.Modal.
func (m *Message) Busy() bool { return false }

// Keys implements ui.Modal.
func (m *Message) Keys() []key.Binding { return []key.Binding{keyEscClose} }

var keyEscClose = key.NewBinding(key.WithKeys("esc", "enter"), key.WithHelp("esc", "close"))

// Update implements ui.Modal.
func (m *Message) Update(msg tea.Msg) (ui.Modal, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && key.Matches(k, keyEscClose) {
		return nil, nil
	}
	return m, nil
}

// Render implements ui.Modal.
func (m *Message) Render(d ui.Display, width, height int) string {
	w := min(width, maxModalWidth/2+10)
	iw, _ := ui.Inner(w, height)
	lines := strings.Split(ansi.Wrap(m.text, iw, " "), "\n")
	lines = append(lines, "", d.Palette.Style(theme.Muted).Render("esc closes"))
	return ui.Panel{Title: m.title, Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

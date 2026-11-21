// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// maxModalWidth keeps previews readable on wide terminals.
const maxModalWidth = 100

// Render implements ui.Modal.
func (c *Confirm) Render(d ui.Display, width, height int) string {
	w := min(width, maxModalWidth)
	iw, _ := ui.Inner(w, height)
	body := c.body(d, iw)
	foot := c.footer(d, iw)
	room := max(height-2-len(foot), 1)
	c.scroll = min(c.scroll, max(len(body)-room, 0))
	shown := body[c.scroll:min(len(body), c.scroll+room)]
	lines := append(append(shown, ""), foot...)
	// The title is the change itself; nothing has happened yet.
	title := fit(safe(c.plan.Summary), iw-2)
	return ui.Panel{Title: title, Focused: true}.Render(d, strings.Join(lines, "\n"), w, min(height, len(lines)+2))
}

// body is the scrollable part: what the plan does and why it may not run.
func (c *Confirm) body(d ui.Display, w int) []string {
	p := c.plan
	st := func(t theme.Token) lipgloss.Style { return d.Palette.Style(t) }
	var out []string
	// add wraps s, keeping its leading spaces on every line it wraps to.
	add := func(style lipgloss.Style, s string) {
		text := strings.TrimLeft(s, " ")
		indent := s[:len(s)-len(text)]
		for _, l := range strings.Split(ansi.Wrap(text, max(w-len(indent), 1), " /@"), "\n") {
			out = append(out, style.Render(indent+l))
		}
	}
	section := func(title string, style lipgloss.Style, items []string) {
		if len(items) == 0 {
			return
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, st(theme.Muted).Render(title))
		for _, it := range items {
			add(style, "  "+it)
		}
	}
	section("Outcome", lipgloss.NewStyle(), texts(p.Preview.Affects))
	section("Destroyed", st(theme.Error), texts(p.Preview.Destroys))
	if p.Preview.Frees > 0 {
		section("Space freed", lipgloss.NewStyle(), []string{format.Size(uint64(p.Preview.Frees), d.Exact)})
	}
	if len(p.Blocked) == 0 { // nothing happens to a blocked change
		section("Cannot be undone", st(theme.Warn), p.Irreversible)
	}
	cmds := make([]string, len(p.Steps))
	for i, a := range p.Steps {
		cmds[i] = a.String()
	}
	heading := "Command"
	if len(cmds) > 1 {
		heading = "Commands"
	}
	section(heading, st(theme.Muted), cmds)
	// zfs's own output, when it says more than the list taken from it.
	if raw := strings.TrimSpace(p.Preview.Output.Raw()); raw != "" && len(p.Preview.Destroys) == 0 {
		// Split before sanitising, which would show the newlines and
		// tabs as escapes; each line is sanitised on its own.
		var lines []string
		for _, l := range strings.Split(raw, "\n") {
			lines = append(lines, termtext.New(strings.ReplaceAll(l, "\t", "  ")).Display())
		}
		section(cmp.Or(p.Preview.OutputTitle, "Dry run"), st(theme.Muted), lines)
	}
	if c.stage == finished {
		out = append(out, "")
		out = append(out, c.result(d, w)...)
	}
	return out
}

func texts(ts []termtext.Text) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Display()
	}
	return out
}

// result reports what ran, what failed and what did not run.
func (c *Confirm) result(d ui.Display, w int) []string {
	r := c.report
	ok, bad, muted := d.Palette.Style(theme.OK), d.Palette.Style(theme.Error), d.Palette.Style(theme.Muted)
	var sc *mutate.StateChangedError
	switch {
	case errors.As(c.err, &sc):
		out := []string{d.Palette.Style(theme.Warn).Render(fit("Nothing was done: this changed since you looked.", w))}
		for _, l := range sc.Changes {
			out = append(out, fit("  "+safe(l), w))
		}
		return out
	case c.err == nil && r.DryRun:
		return []string{ok.Render(fit("Dry run: written to the audit log, nothing changed.", w))}
	case c.err == nil && r.Simulated:
		return []string{ok.Render(fit("Done. This is the demo: only its own copy of the pools changed.", w))}
	case c.err == nil:
		out := []string{ok.Render("Done.")}
		for _, s := range r.Ran { // what zfs said although it worked, unless expected
			msg := strings.TrimSpace(s.Stderr.Raw())
			if msg != "" && (s.Argv.Done == "" || !strings.Contains(msg, s.Argv.Done)) {
				out = append(out, muted.Render(fit("zfs says: "+safe(msg), w)))
			}
		}
		return out
	case r.Failed == nil && len(r.Ran) == 0:
		return []string{bad.Render(fit(safe(c.err.Error()), w))}
	}
	// Something failed: say what ran, what failed and why, what did not run.
	var out []string
	for _, s := range r.Ran {
		out = append(out, ok.Render(fit("ran: "+s.Argv.String(), w)))
	}
	if r.Failed != nil {
		out = append(out, bad.Render(fit(fmt.Sprintf("failed (exit %d): %s", r.Failed.ExitCode, r.Failed.Argv), w)))
		for _, l := range strings.Split(strings.TrimSpace(r.Failed.Stderr.Display()), "\n") {
			out = append(out, bad.Render(fit("  "+l, w)))
		}
	} else {
		out = append(out, bad.Render(fit(safe(c.err.Error()), w)))
	}
	for _, a := range r.NotRun {
		out = append(out, muted.Render(fit("not run: "+a.String(), w)))
	}
	return out
}

func fit(s string, w int) string { return ansi.Truncate(s, w, "…") }

// sentence starts s with a capital letter: errors are lower case by Go
// convention, messages on screen are sentences.
func sentence(s string) string {
	if s == "" || strings.HasPrefix(s, "fstui") { // a name stays as it is
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// safe makes text from errors and reasons fit to show: they can carry
// names, property values and command output, which are untrusted.
func safe(s string) string { return termtext.New(s).Display() }

// footer is the fixed part: the mode, and how to confirm.
func (c *Confirm) footer(d ui.Display, w int) []string {
	muted, warn := d.Palette.Style(theme.Muted), d.Palette.Style(theme.Warn)
	switch c.stage {
	case running:
		return []string{warn.Render("Running. This cannot be stopped part way; wait for the result.")}
	case finished:
		if c.changed && c.rebuild != nil {
			return []string{muted.Render("r looks again at the system as it is now. esc closes.")}
		}
		return []string{muted.Render("esc closes")}
	case reviewing:
	}
	if err := c.blocked(); err != nil {
		out := wrapStyled(warn, sentence(safe(err.Error())), w)
		if c.saved != "" {
			out = append(out, d.Palette.Style(theme.Accent).Render(fit(safe(c.saved), w)))
		}
		hint := "esc closes"
		if c.save != nil && len(c.plan.Blocked) == 0 && c.saved == "" {
			hint = "ctrl+s saves it for later, in Plans. esc closes."
		}
		return append(out, muted.Render(hint))
	}
	var out []string
	if c.saved != "" {
		out = append(out, d.Palette.Style(theme.Accent).Render(fit(safe(c.saved), w)))
	}
	if line := modeLine(c.run.CurrentMode()); line != "" {
		out = append(out, d.Palette.Style(theme.Accent).Render(fit(line, w)))
	}
	switch c.tier {
	case mutate.High:
		return append(out, c.highFooter(d, w)...)
	case mutate.Medium:
		return append(out, c.buttons(d), muted.Render("y selects "+c.verb()+", enter presses the selected button, esc or n cancels"))
	case mutate.Low:
	}
	return append(out, d.Palette.Style(theme.Accent).Bold(true).Render("y")+muted.Render(" or enter: "+strings.ToLower(c.verb())+", n or esc: cancel"))
}

func (c *Confirm) buttons(d ui.Display) string {
	focus := d.Palette.Style(theme.Accent).Bold(true).Reverse(true)
	plain := d.Palette.Style(theme.Muted)
	cancel, confirm := focus.Render(" Cancel "), plain.Render(" "+c.verb()+" ")
	if c.confirm {
		cancel, confirm = plain.Render(" Cancel "), focus.Render(" "+c.verb()+" ")
	}
	return cancel + "  " + confirm
}

func (c *Confirm) highFooter(d ui.Display, w int) []string {
	muted := d.Palette.Style(theme.Muted)
	want := termtext.New(c.expected()).Display()
	state := d.Palette.Style(theme.Warn).Render("does not match")
	if c.matches() {
		state = d.Palette.Style(theme.OK).Render("matches; enter to proceed")
	}
	typed := termtext.New(c.typed).Display()
	return []string{
		fit("To proceed, type "+lipgloss.NewStyle().Bold(true).Render(want), w),
		fit("> "+typed+ui.Cursor(d)+"  "+state, w),
		muted.Render("esc cancels"),
	}
}

// modeLine says what confirming will do in dry-run mode; read-only
// mode blocks the plan and says so there.
func modeLine(mode execute.Mode) string {
	if mode == execute.DryRun {
		return "Dry run: nothing will change. The command goes in the audit log."
	}
	return ""
}

// verb is the word on the button that proceeds: the first word of
// the change's summary ("Destroy", "Rename"), or Confirm.
func (c *Confirm) verb() string {
	v, _, _ := strings.Cut(c.plan.Summary, " ")
	if v == "" || strings.ContainsFunc(v, func(r rune) bool { return r < 'A' || r > 'z' }) {
		return "Confirm"
	}
	return v
}

// modeWord is the status bar's word for a mode.
func modeWord(mode execute.Mode) string {
	switch mode {
	case execute.Write:
		return "WRITE"
	case execute.DryRun:
		return "DRY RUN"
	case execute.ReadOnly:
	}
	return "READ-ONLY"
}

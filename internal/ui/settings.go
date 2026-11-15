// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// settingsView is the view number of Settings.
const settingsView = 9

// Settings edits the user's config.yaml from the UI. Changes stay pending
// until the user reviews them: they are written to a private temporary file
// and shown as a diff; only a confirmation replaces the file,
// keeping the previous one as a backup that revert restores.
type Settings struct {
	file      string // the user's config.yaml
	files     []string
	cfg       config.Config
	list      []config.Setting
	cursor    int
	pending   map[string]string
	input     *numberInput // editing a number
	review    *review      // a staged file awaiting confirmation
	message   string       // result of the last save, revert or error
	term      string       // TERM, for ascii: auto
	load      Loader
	loadTheme func(string) (theme.Theme, []config.Problem)
}

// Loader reads the config files in order; config.Load in fstui.
type Loader func(files ...string) (config.Config, []config.Problem)

type numberInput struct {
	path string
	text string
}

// review is a staged file and what it changes.
type review struct {
	staged   string
	diff     []string
	problems []config.Problem
	revert   bool
}

// NewSettings returns the Settings view for the user's config file; files
// are all config files in load order, cfg the loaded settings.
func NewSettings(files []string, cfg config.Config, themes []string, load Loader,
	loadTheme func(string) (theme.Theme, []config.Problem),
) *Settings {
	return &Settings{
		file: files[len(files)-1], files: files, cfg: cfg,
		list: config.Settings(themes), pending: map[string]string{},
		term: os.Getenv("TERM"), load: load, loadTheme: loadTheme,
	}
}

// Number implements Numbered.
func (s *Settings) Number() int { return settingsView }

// Name implements Screen.
func (s *Settings) Name() string { return "Settings" }

// Init implements Screen.
func (s *Settings) Init() tea.Cmd { return nil }

// Nested implements Screen: esc leaves an edit or a review first.
func (s *Settings) Nested() bool { return s.input != nil || s.review != nil }

// Typing implements Typing: while a number is typed, digits are text.
func (s *Settings) Typing() bool { return s.input != nil }

// Status implements Screen.
func (s *Settings) Status() Status {
	if n := len(s.pending); n > 0 {
		return Status{Static: true, Figure: fmt.Sprintf("%d unsaved %s", n, map[bool]string{true: "change", false: "changes"}[n == 1])}
	}
	return Status{Static: true}
}

// Keys implements Screen.
func (s *Settings) Keys() []key.Binding {
	switch {
	case s.review != nil:
		return []key.Binding{keys.Get(keys.Write), keys.As(keys.Cancel, "keep the old file")}
	case s.input != nil:
		return []key.Binding{keys.As(keys.Select, "set"), cancelTyping()}
	}
	return []key.Binding{
		keys.As(keys.Select, "change"), keys.Get(keys.Save), keys.Get(keys.Discard), keys.Get(keys.Revert),
		keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"),
	}
}

// Update implements Screen.
func (s *Settings) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return s, s.key(msg)
	case ScrollMsg:
		s.cursor = max(min(s.cursor+msg.Delta, len(s.list)-1), 0)
	case ClickMsg:
		if row := msg.Y - settingsTop; s.input == nil && s.review == nil && row >= 0 && row < len(s.list) {
			s.cursor = row
		}
	}
	return s, nil
}

func (s *Settings) key(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case s.review != nil:
		return s.reviewKey(msg)
	case s.input != nil:
		s.inputKey(msg)
		return nil
	case key.Matches(msg, keys.Get(keys.Down)):
		s.cursor = min(s.cursor+1, len(s.list)-1)
	case key.Matches(msg, keys.Get(keys.Up)):
		s.cursor = max(s.cursor-1, 0)
	case key.Matches(msg, keys.Get(keys.Select)):
		s.change(s.list[s.cursor])
		if s.list[s.cursor].Path == "theme" {
			return s.preview()
		}
	case key.Matches(msg, keys.Get(keys.Discard)):
		s.pending, s.message = map[string]string{}, "Unsaved changes discarded."
		return s.preview()
	case key.Matches(msg, keys.Get(keys.Save)):
		s.startReview()
	case key.Matches(msg, keys.Get(keys.Revert)):
		s.startRevert()
	}
	return nil
}

// preview shows the theme the user would get: the pending one while it
// is unsaved, else the saved one.
func (s *Settings) preview() tea.Cmd { return s.showTheme(s.value("theme")) }

func (s *Settings) showTheme(name string) tea.Cmd {
	th, problems := s.loadTheme(name)
	if len(problems) > 0 {
		s.message = problems[0].String()
	}
	return func() tea.Msg { return ThemeMsg{Theme: th} }
}

// Activate implements Activator: an unsaved theme is shown again.
func (s *Settings) Activate() tea.Cmd {
	if _, ok := s.pending["theme"]; ok {
		return s.preview()
	}
	return nil
}

// Deactivate implements Deactivator: leaving Settings with an unsaved
// theme shows the saved one, so other views look as they will after a
// restart.
func (s *Settings) Deactivate() tea.Cmd {
	if _, ok := s.pending["theme"]; ok {
		return s.showTheme(s.cfg.Theme)
	}
	return nil
}

// value is a setting's value as the user will get it: pending, or loaded.
func (s *Settings) value(path string) string {
	if v, ok := s.pending[path]; ok {
		return v
	}
	return s.cfg.Value(path)
}

// change cycles a choice to its next value, or starts typing a number.
func (s *Settings) change(set config.Setting) {
	s.message = ""
	if s.cfg.Sources[set.Path] == config.FlagSource {
		s.message = set.Path + " was given on the command line and stays as given for this session."
		return
	}
	if set.Choices == nil {
		s.input = &numberInput{path: set.Path, text: s.value(set.Path)}
		return
	}
	next := set.Choices[(slices.Index(set.Choices, s.value(set.Path))+1)%len(set.Choices)]
	s.setPending(set.Path, next)
}

func (s *Settings) setPending(path, v string) {
	if v == s.cfg.Value(path) {
		delete(s.pending, path)
		return
	}
	s.pending[path] = v
}

func (s *Settings) inputKey(msg tea.KeyPressMsg) {
	in := s.input
	switch {
	case key.Matches(msg, cancelTyping()):
		s.input = nil
	case msg.String() == "enter":
		set := s.list[slices.IndexFunc(s.list, func(x config.Setting) bool { return x.Path == in.path })]
		n, err := strconv.Atoi(in.text)
		if err != nil || n < set.Min || n > set.Max {
			s.message = fmt.Sprintf("%s must be a whole number from %d to %d.", set.Path, set.Min, set.Max)
			return
		}
		s.setPending(in.path, strconv.Itoa(n))
		s.input = nil
	case msg.String() == "backspace" && in.text != "":
		in.text = in.text[:len(in.text)-1]
	case len(msg.Text) == 1 && msg.Text[0] >= '0' && msg.Text[0] <= '9' && len(in.text) < maxDigits:
		in.text += msg.Text
	}
}

// cancelTyping is Back without q, which is text while typing.
func cancelTyping() key.Binding {
	ks := slices.DeleteFunc(slices.Clone(keys.Get(keys.Back).Keys()), func(k string) bool { return k == "q" })
	return key.NewBinding(key.WithKeys(ks...), key.WithHelp(strings.Join(ks, "/"), "cancel"))
}

// maxDigits bounds a typed number; the largest setting is 3600.
const maxDigits = 6

// startReview writes the pending changes to a temporary file and shows
// what they change; nothing replaces config.yaml until confirmed.
func (s *Settings) startReview() {
	if len(s.pending) == 0 {
		s.message = "No unsaved changes."
		return
	}
	cur, err := config.Read(s.file)
	if err != nil {
		s.message = err.Error()
		return
	}
	next, err := config.Edit(cur, s.pending)
	if err != nil {
		s.message = err.Error()
		return
	}
	s.stage(cur, next, false)
}

// startRevert shows what restoring the previous file would change.
func (s *Settings) startRevert() {
	bak, err := config.Read(config.Backup(s.file))
	switch {
	case err != nil:
		s.message = err.Error()
	case bak == nil:
		s.message = "Nothing to revert: no previous version of " + s.file + " has been kept yet."
	default:
		cur, err := config.Read(s.file)
		if err != nil {
			s.message = err.Error()
			return
		}
		s.stage(cur, bak, true)
	}
}

func (s *Settings) stage(cur, next []byte, revert bool) {
	staged, err := config.Stage(s.file, next)
	if err != nil {
		s.message = err.Error()
		return
	}
	s.review = &review{staged: staged, diff: config.Diff(cur, next), problems: config.Check(s.file, next), revert: revert}
	s.message = ""
}

func (s *Settings) reviewKey(msg tea.KeyPressMsg) tea.Cmd {
	r := s.review
	switch {
	case key.Matches(msg, keys.Get(keys.Write)) && len(r.problems) == 0:
		s.review = nil
		if err := config.Promote(s.file, r.staged); err != nil {
			s.message = err.Error()
			return nil
		}
		s.pending = map[string]string{}
		return s.reload(r.revert)
	case key.Matches(msg, keys.Get(keys.Cancel)) || key.Matches(msg, keys.Get(keys.Back)):
		s.review = nil
		if err := config.Discard(r.staged); err != nil {
			s.message = err.Error()
			return nil
		}
		s.message = "Not saved; " + s.file + " is unchanged."
	}
	return nil
}

// reload reads the saved settings and hands them to the root and the
// views, so they apply without restarting.
func (s *Settings) reload(reverted bool) tea.Cmd {
	cfg, problems := s.load(s.files...)
	th, themeProblems := s.loadTheme(cfg.Theme)
	s.cfg = cfg
	verb := "Saved"
	if reverted {
		verb = "Reverted"
	}
	s.message = fmt.Sprintf("%s %s; the previous version is %s (%s reverts).", verb, s.file, config.Backup(s.file), keys.Get(keys.Revert).Keys()[0])
	if all := append(problems, themeProblems...); len(all) > 0 {
		s.message += " " + all[0].String()
	}
	msg := ConfigMsg{Config: cfg, Theme: th, ASCII: cfg.UseASCII(s.term)}
	return func() tea.Msg { return msg }
}

// sourceOf names where a setting's loaded value comes from.
func (s *Settings) sourceOf(path string) string {
	if _, ok := s.pending[path]; ok {
		return "unsaved"
	}
	if f, ok := s.cfg.Sources[path]; ok {
		return f
	}
	return "default"
}

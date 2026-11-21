// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// settingsView is the view number of Config, whose General tab Settings is.
const settingsView = 9

// Settings edits the user's config.yaml from the UI. Changes stay pending
// until the user reviews them: they are written to a private temporary file
// and shown as a diff; only a confirmation replaces the file,
// keeping the previous one as a backup that revert restores.
type Settings struct {
	file   string // the user's config.yaml
	files  []string
	cfg    config.Config
	list   []config.Spec
	cursor int // index into list of the selected setting
	// section is the open group of settings (config.Sections), or -1
	// for the list of groups, where group is the selected one; top is
	// the first row drawn of a long group.
	section, group, top int
	pending             map[string]string
	input               *numberInput // editing a number
	review              *review      // staged files awaiting confirmation
	lastSaved           []string     // the files the last save wrote, which u reverts
	message             string       // result of the last save, revert or error
	term                string       // TERM, for ascii: auto
	load                Loader
	loadTheme           func(string) (theme.Theme, []config.Problem)
}

// Loader reads the config files in order; config.Load in fstui.
type Loader func(files ...string) (config.Config, []config.Problem)

type numberInput struct {
	path string
	text string
}

// review is the staged files and what each changes: an edit goes to the
// user's file that sets the key, which may be a drop-in.
type review struct {
	files    []reviewFile
	problems []config.Problem
	revert   bool
}

// reviewFile is one file of a review.
type reviewFile struct {
	file, staged string
	data         []byte // what was staged and shown; saved as is
	diff         []string
}

// NewSettings returns the Config view's General tab for the user's config file user;
// files are all config files in load order, cfg the loaded settings. user
// is empty when fstui could not find the user's file: then nothing can be
// saved, and the system file is never edited in its place.
func NewSettings(user string, files []string, cfg config.Config, themes []string, load Loader,
	loadTheme func(string) (theme.Theme, []config.Problem),
) *Settings {
	return &Settings{
		file: user, files: files, cfg: cfg,
		list: config.Settings(themes), pending: map[string]string{}, section: -1,
		term: os.Getenv("TERM"), load: load, loadTheme: loadTheme,
	}
}

// Nested implements Screen: esc leaves an edit, a review or an open
// group first.
func (s *Settings) Nested() bool { return s.input != nil || s.review != nil || s.section >= 0 }

// items are the indices in list of the open group's settings, in the
// order the group names them.
func (s *Settings) items() []int {
	var out []int
	for i, set := range s.list {
		if config.SectionOf(set.Path) == s.section {
			out = append(out, i)
		}
	}
	slices.SortStableFunc(out, func(a, b int) int {
		return config.PlaceIn(s.list[a].Path) - config.PlaceIn(s.list[b].Path)
	})
	return out
}

// open shows group g's settings, the first selected.
func (s *Settings) open(g int) {
	s.section, s.group, s.top = g, g, 0
	if it := s.items(); len(it) > 0 {
		s.cursor = it[0]
	}
}

// move moves the selection by delta within the open group, or among the
// groups; to is a row to jump to when not -1.
func (s *Settings) move(delta, to int) {
	if s.section < 0 {
		n := len(config.Sections)
		if to >= 0 {
			s.group = min(to, n-1)
		} else {
			s.group = max(min(s.group+delta, n-1), 0)
		}
		return
	}
	it := s.items()
	i := slices.Index(it, s.cursor)
	if to >= 0 {
		i = min(to, len(it)-1)
	} else {
		i = max(min(i+delta, len(it)-1), 0)
	}
	s.cursor = it[i]
}

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
	case s.section < 0:
		return []key.Binding{
			keys.As(keys.Select, "open"), keys.Get(keys.Save), keys.Get(keys.Discard), keys.Get(keys.Revert),
			keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"),
		}
	}
	return []key.Binding{
		keys.As(keys.Select, "change"), keys.As(keys.Back, "all groups"), keys.Get(keys.Save), keys.Get(keys.Discard), keys.Get(keys.Revert),
		keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"),
	}
}

// Update handles the tab's keys and clicks.
func (s *Settings) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return s.key(msg)
	case ScrollMsg:
		if s.input == nil && s.review == nil {
			s.move(msg.Delta, -1)
		}
	case ClickMsg:
		if row := msg.Y - settingsTop; s.input == nil && s.review == nil && row >= 0 {
			s.click(row)
		}
	}
	return nil
}

// click selects the row clicked; in the list of groups it opens one.
func (s *Settings) click(row int) {
	if s.section < 0 {
		if row < len(config.Sections) {
			s.open(row)
		}
		return
	}
	if row+s.top < len(s.items()) {
		s.move(0, row+s.top)
	}
}

func (s *Settings) key(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case s.review != nil:
		return s.reviewKey(msg)
	case s.input != nil:
		s.inputKey(msg)
		return nil
	case key.Matches(msg, keys.Get(keys.Down)):
		s.move(1, -1)
	case key.Matches(msg, keys.Get(keys.Up)):
		s.move(-1, -1)
	case key.Matches(msg, keys.Get(keys.First)):
		s.move(0, 0)
	case key.Matches(msg, keys.Get(keys.Last)):
		s.move(0, len(s.list))
	case key.Matches(msg, keys.Get(keys.Back)) && s.section >= 0:
		s.section = -1
	case key.Matches(msg, keys.Get(keys.Select)) && s.section < 0:
		s.open(s.group)
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

// noFile says why nothing can be saved, when the user's file is unknown.
func (s *Settings) noFile() bool {
	if s.file == "" {
		s.message = "Changes cannot be saved: fstui could not find your config file (the notice at startup says why)."
		return true
	}
	return false
}

// change steps a setting to its next value, or starts typing one.
func (s *Settings) change(set config.Spec) {
	s.message = ""
	if s.noFile() {
		return
	}
	if s.cfg.Sources[set.Path] == config.FlagSource {
		s.message = set.Path + " was given on the command line and stays as given for this session."
		return
	}
	if set.Typed() {
		s.input = &numberInput{path: set.Path, text: s.value(set.Path)}
		return
	}
	opts := set.Options()
	if len(opts) == 0 {
		return
	}
	s.setPending(set.Path, opts[(slices.Index(opts, s.value(set.Path))+1)%len(opts)])
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
		set, _ := config.SpecFor(in.path)
		if err := set.Check(in.text); err != nil {
			s.message = set.Path + ": " + err.Error() + "."
			return
		}
		s.setPending(in.path, in.text)
		s.input = nil
	case msg.String() == "backspace" && in.text != "":
		in.text = in.text[:len(in.text)-1]
	case len(msg.Text) == 1 && typable(in.path, msg.Text) && len(in.text) < maxTyped:
		in.text += msg.Text
	}
}

// typable reports whether a character may be typed into a setting:
// digits and duration units for numbers, any printable one for text.
func typable(path, ch string) bool {
	set, _ := config.SpecFor(path)
	if set.Kind == config.KindString {
		r := []rune(ch)[0]
		return r >= ' ' && r != 0x7f
	}
	return strings.ContainsAny(ch, "0123456789.-hmsu")
}

// cancelTyping is Back without q, which is text while typing.
func cancelTyping() key.Binding {
	ks := slices.DeleteFunc(slices.Clone(keys.Get(keys.Back).Keys()), func(k string) bool { return k == "q" })
	return key.NewBinding(key.WithKeys(ks...), key.WithHelp(strings.Join(ks, "/"), "cancel"))
}

// maxTyped bounds a typed value: a number, a duration such as 720h, or
// a host name.
const maxTyped = 64

// startReview writes the pending changes to temporary files, one per
// file they go to, and shows what they change; nothing is replaced until
// confirmed.
func (s *Settings) startReview() {
	if s.noFile() {
		return
	}
	if len(s.pending) == 0 {
		s.message = "No unsaved changes."
		return
	}
	byFile := map[string]map[string]string{}
	for path, v := range s.pending {
		t := s.cfg.Target(s.file, path)
		if byFile[t] == nil {
			byFile[t] = map[string]string{}
		}
		byFile[t][path] = v
	}
	r := &review{}
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		cur, err := config.Read(file)
		if err == nil {
			var next []byte
			if next, err = config.Edit(cur, byFile[file]); err == nil {
				err = s.stageFile(r, file, cur, next)
			}
		}
		if err != nil {
			s.discard(r)
			s.message = err.Error()
			return
		}
	}
	s.review, s.message = r, ""
}

// startRevert shows what restoring the files of the last save, or the
// user's config.yaml, to their previous versions would change.
func (s *Settings) startRevert() {
	if s.noFile() {
		return
	}
	files := s.lastSaved
	if len(files) == 0 {
		files = []string{s.file}
	}
	r := &review{revert: true}
	for _, file := range files {
		bak, err := config.Read(config.Backup(file))
		if err == nil && bak == nil {
			continue
		}
		cur, rerr := config.Read(file)
		if err = cmp.Or(err, rerr); err == nil {
			err = s.stageFile(r, file, cur, bak)
		}
		if err != nil {
			s.discard(r)
			s.message = err.Error()
			return
		}
	}
	if len(r.files) == 0 {
		s.message = "Nothing to revert: no previous version of " + strings.Join(files, ", ") + " has been kept yet."
		return
	}
	s.review, s.message = r, ""
}

func (s *Settings) stageFile(r *review, file string, cur, next []byte) error {
	staged, err := config.Stage(file, next)
	if err != nil {
		return err // config names the file
	}
	r.files = append(r.files, reviewFile{file: file, staged: staged, data: next, diff: config.Diff(cur, next)})
	r.problems = append(r.problems, config.Check(file, next)...)
	return nil
}

// discard removes a review's staged copies.
func (s *Settings) discard(r *review) {
	for _, f := range r.files {
		if err := config.Discard(f.staged); err != nil {
			s.message = err.Error()
		}
	}
}

func (s *Settings) reviewKey(msg tea.KeyPressMsg) tea.Cmd {
	r := s.review
	switch {
	case key.Matches(msg, keys.Get(keys.Write)) && len(r.problems) == 0:
		s.review = nil
		var saved []string
		for _, f := range r.files {
			if err := config.Promote(f.file, f.staged, f.data); err != nil {
				s.message = err.Error()
				return nil
			}
			saved = append(saved, f.file)
		}
		s.pending, s.lastSaved = map[string]string{}, saved
		return s.reload(r.revert)
	case key.Matches(msg, keys.Get(keys.Cancel)) || key.Matches(msg, keys.Get(keys.Back)):
		s.review = nil
		s.discard(r)
		if s.message == "" {
			s.message = "Not saved; the files are unchanged."
		}
	}
	return nil
}

// reload reads the settings again, drop-ins included, and hands them to
// the root and the views, so they apply without restarting.
func (s *Settings) reload(reverted bool) tea.Cmd {
	files := s.files
	if s.file != "" {
		files = config.Files(s.file)
	}
	cfg, problems := s.load(files...)
	th, themeProblems := s.loadTheme(cfg.Theme)
	s.cfg = cfg
	verb := "Saved"
	if reverted {
		verb = "Reverted"
	}
	s.message = fmt.Sprintf("%s %s; the previous versions are kept as .bak (%s reverts).", verb, strings.Join(s.lastSaved, ", "), keyName(keys.Revert))
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

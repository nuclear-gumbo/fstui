// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// channelEdit is the tab's unsaved work: the channel list as it will be,
// and the secrets to set or remove.
type channelEdit struct {
	channels []config.Channel
	set      map[string]string
	remove   []string
}

// channelReview is a new config.yaml, or the drop-in that lists the
// channels, staged, and the secrets that change with it, awaiting w.
type channelReview struct {
	*fileReview
	secrets []string // "phone: set", "chat: removed"
}

// savedMsg tells the Config view that config.yaml changed.
type savedMsg struct{}

// target is the file the channels are written to: the user's file that
// lists them now, which may be a drop-in such as notifications.yaml, else
// config.yaml.
func (n *Notifications) target() string { return n.cfg.Target(n.file, "notifications.channels") }

// secretsFile is the secrets file next to the user's config.yaml.
func (n *Notifications) secretsFile() string {
	return filepath.Join(filepath.Dir(n.file), config.SecretsName)
}

// channels are the channels as they will be after saving.
func (n *Notifications) channels() []config.Channel {
	if n.edit != nil {
		return n.edit.channels
	}
	return n.cfg.Notifications.Channels
}

func (n *Notifications) startEdit() *channelEdit {
	if n.edit == nil {
		n.edit = &channelEdit{channels: slices.Clone(n.cfg.Notifications.Channels), set: map[string]string{}}
	}
	return n.edit
}

// editKey handles a, enter, d and p outside the form, and reports
// whether the key was one of them.
func (n *Notifications) editKey(msg tea.KeyPressMsg) bool {
	chs := n.channels()
	switch {
	case key.Matches(msg, keys.Get(keys.Actions)) || key.Matches(msg, keys.Get(keys.Select)):
		if n.file == "" {
			n.message = "Channels cannot be changed: fstui could not find your config file (the notice at startup says why)."
			return true
		}
		ch := config.Channel{}
		if key.Matches(msg, keys.Get(keys.Select)) && len(chs) > 0 {
			ch = chs[n.cursor]
		}
		n.form, n.message = newChannelForm(ch), ""
	case key.Matches(msg, keys.Get(keys.Discard)) && len(chs) > 0:
		name := chs[n.cursor].Name
		if n.deleteArmed != name {
			n.deleteArmed, n.message = name, "Press "+keyName(keys.Discard)+" again to remove "+name+"."
			return true
		}
		e := n.startEdit()
		e.channels = slices.Delete(e.channels, n.cursor, n.cursor+1)
		e.remove = append(e.remove, name)
		delete(e.set, name)
		n.cursor, n.deleteArmed, n.message = max(min(n.cursor, len(e.channels)-1), 0), "", name+" removed; "+keyName(keys.Save)+" reviews the change."
	case key.Matches(msg, keys.Get(keys.Save)) && n.edit != nil:
		n.review = n.stageReview()
	default:
		return false
	}
	return true
}

// finishForm applies the form to the unsaved channels and reviews them.
func (n *Notifications) finishForm(f *channelForm) {
	ch := f.channel()
	e := n.startEdit()
	if i := slices.IndexFunc(e.channels, func(c config.Channel) bool { return c.Name == f.editing && f.editing != "" }); i >= 0 {
		e.channels[i] = ch
		n.cursor = i
	} else {
		e.channels = append(e.channels, ch)
		n.cursor = len(e.channels) - 1
	}
	if f.editing != "" && f.editing != ch.Name {
		e.remove = append(e.remove, f.editing) // renamed: the secret moves
		if _, kept := e.set[f.editing]; !kept && f.secret == "" {
			if v, err := n.currentSecret(f.editing); err == nil {
				e.set[ch.Name] = v
			}
		}
	}
	if f.secret != "" {
		e.set[ch.Name] = f.secret
		e.remove = slices.DeleteFunc(e.remove, func(r string) bool { return r == ch.Name })
	}
	n.review = n.stageReview()
}

// currentSecret is a channel's saved secret, to move it with a rename.
func (n *Notifications) currentSecret(name string) (string, error) {
	s, _ := n.secrets()
	v, ok := s[name]
	if !ok {
		return "", config.ErrNoSecret
	}
	return v, nil
}

// stageReview writes the new config.yaml to a private temporary file and
// lists what changes; the secrets are named, never shown.
func (n *Notifications) stageReview() *channelReview {
	e := n.edit
	file := n.target()
	cur, err := config.Read(file)
	if err != nil {
		n.message = err.Error()
		return nil
	}
	next, err := config.EditChannels(cur, e.channels)
	if err != nil {
		n.message = err.Error()
		return nil
	}
	if string(next) == string(cur) && len(e.set) == 0 && len(e.remove) == 0 {
		n.edit, n.message = nil, "No changes to save."
		return nil
	}
	fr, err := newFileReview(n.file, file, cur, next)
	if err != nil {
		n.message = err.Error()
		return nil
	}
	r := &channelReview{fileReview: fr}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range e.set {
			if !yield(k) {
				return
			}
		}
	}) {
		r.secrets = append(r.secrets, name+": set")
	}
	for _, name := range e.remove {
		if _, set := e.set[name]; !set {
			r.secrets = append(r.secrets, name+": removed")
		}
	}
	return r
}

func (n *Notifications) reviewKey(msg tea.KeyPressMsg) tea.Cmd {
	r := n.review
	switch {
	case key.Matches(msg, keys.Get(keys.Write)) && len(r.problems) == 0:
		n.review = nil
		if err := n.write(r); err != nil {
			n.message = err.Error()
			return nil
		}
		n.edit, n.message = nil, "Saved "+r.file+"; the previous version is "+config.Backup(r.file)+"."
		return func() tea.Msg { return savedMsg{} }
	case key.Matches(msg, keys.Get(keys.Cancel)) || key.Matches(msg, keys.Get(keys.Back)):
		n.review = nil
		if err := config.Discard(r.staged); err != nil {
			n.message = err.Error()
			return nil
		}
		n.message = "Not saved; " + r.file + " is unchanged. " + keyName(keys.Discard) + " on a channel or " + keyMark("esc") + " here keeps editing."
	}
	return nil
}

// write saves the secrets, then config.yaml: a channel never points at
// a secret that is not there yet. The secrets are written in place, not
// staged in a temporary directory.
func (n *Notifications) write(r *channelReview) error {
	e := n.edit
	if len(e.set) > 0 || len(e.remove) > 0 {
		file := n.secretsFile()
		cur, err := config.Read(file)
		if err != nil {
			return err
		}
		next, err := config.EditSecrets(cur, e.set, e.remove)
		if err != nil {
			return err
		}
		if err := config.Promote(file, "", next); err != nil {
			return err
		}
	}
	return config.Promote(r.file, r.staged, r.data)
}

func (n *Notifications) renderForm(d Display, width, height int) string {
	f := n.form
	return f.render(d, width, height, func(fl formField) (string, bool) {
		if fl.secret && f.secret == "" {
			return n.secretShown(f), true
		}
		return "", false
	})
}

// secretShown says whether a channel has a secret, never what it is.
func (n *Notifications) secretShown(f *channelForm) string {
	switch {
	case f.secret != "":
		return "new, not saved yet"
	case n.edit != nil && n.edit.set[f.editing] != "":
		return "changed, not saved yet"
	}
	if _, err := n.currentSecret(f.editing); err == nil && f.editing != "" {
		return "set"
	}
	return "not set"
}

func (n *Notifications) renderReview(d Display, width, height int) string {
	r := n.review
	var extra []string
	if len(r.secrets) > 0 {
		extra = []string{"", ansi.Wrap("In "+n.secretsFile()+" (mode 0600): "+strings.Join(r.secrets, ", ")+".", width, "")}
	}
	return r.render(d, width, height, extra)
}

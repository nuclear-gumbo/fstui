// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"reflect"
	"strings"
	"testing"
)

// Channels written into a file read back the same, and the rest of the
// file stays.
func TestEditChannelsRoundTrip(t *testing.T) {
	cur := []byte("# my settings\ntheme: gruvbox\nnotifications:\n  repeat: 6h\n  channels: []\n")
	chs := []Channel{
		{Name: "phone", Type: "ntfy", URL: "https://ntfy.example.org", Topic: "zfs", User: "alice", MinSeverity: "warning"},
		{Name: "mail", Type: "smtp", Host: "mail.example.org", Port: 465, From: "f@example.org", To: []string{"a@example.org", "b@example.org"}, MinSeverity: "error"},
		{Name: "lan", Type: "gotify", URL: "http://gotify.lan", InsecureHTTP: true, MinSeverity: "info"},
	}
	next, err := EditChannels(cur, chs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(next), "# my settings\ntheme: gruvbox\n") {
		t.Errorf("rest of the file:\n%s", next)
	}
	c, problems := Parse("config.yaml", next)
	if len(problems) != 0 || c.Notifications.Repeat.Hours() != 6 || !reflect.DeepEqual(c.Notifications.Channels, chs) {
		t.Errorf("read back %+v, %v:\n%s", c.Notifications.Channels, problems, next)
	}
}

func TestEditSecrets(t *testing.T) {
	next, err := EditSecrets([]byte("old: a\nkeep: b\n"), map[string]string{"new": "p:a ss\"word", "keep": "c"}, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	s := Secrets{}
	_, root, _ := document(next)
	if problems := secretEntries("s", root, s); len(problems) != 0 || len(s) != 2 || s["new"] != "p:a ss\"word" || s["keep"] != "c" {
		t.Errorf("%v %v:\n%s", s, problems, next)
	}
	if _, err := EditSecrets([]byte("a: [unclosed s3cret"), nil, nil); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("broken file: %v", err)
	}
}

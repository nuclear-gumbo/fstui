// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Each service gets the request its API documents, with text from ZFS
// unable to act as markup or a mention.
func TestChatServices(t *testing.T) {
	m := Message{Title: "host: tank is DEGRADED", Body: "@everyone <!channel> & more", Level: Error}
	for _, c := range []struct {
		typ    string
		setup  func(*Channel)
		check  func(t *testing.T, s seen)
		header string
	}{
		{"gotify", func(c *Channel) { c.Secret = "app" }, func(t *testing.T, s seen) {
			var b map[string]any
			_ = json.Unmarshal(s.body, &b)
			if s.path != "/message" || b["priority"] != float64(8) || b["title"] != m.Title {
				t.Errorf("gotify %s %s", s.path, s.body)
			}
		}, ""},
		{"pushover", func(c *Channel) { c.Secret, c.UserKey = "app", "user" }, func(t *testing.T, s seen) {
			v, _ := url.ParseQuery(string(s.body))
			if v.Get("token") != "app" || v.Get("user") != "user" || v.Get("priority") != "1" || v.Get("message") != m.Body {
				t.Errorf("pushover %s", s.body)
			}
		}, ""},
		{"telegram", func(c *Channel) { c.Secret, c.ChatID = "123:abc", "-100" }, func(t *testing.T, s seen) {
			var b map[string]any
			_ = json.Unmarshal(s.body, &b)
			if s.path != "/bot123:abc/sendMessage" || b["chat_id"] != "-100" || b["parse_mode"] != nil || !strings.HasPrefix(b["text"].(string), m.Title+"\n") { //nolint:forcetypeassert // the test fails on a panic too
				t.Errorf("telegram %s %s", s.path, s.body)
			}
		}, ""},
		{"discord", func(c *Channel) { c.Secret, c.URL = c.URL+"/api/webhooks/1/x", "" }, func(t *testing.T, s seen) {
			if !strings.Contains(string(s.body), `"allowed_mentions":{"parse":[]}`) {
				t.Errorf("discord %s", s.body)
			}
		}, ""},
		{"slack", func(c *Channel) { c.Secret, c.URL = c.URL+"/services/T/B/x", "" }, func(t *testing.T, s seen) {
			var b map[string]string
			_ = json.Unmarshal(s.body, &b)
			if !strings.Contains(b["text"], "&lt;!channel&gt; &amp; more") {
				t.Errorf("slack %s", s.body)
			}
		}, ""},
		{"matrix", func(c *Channel) { c.Secret, c.Room = "tok", "!room:example.org" }, func(t *testing.T, s seen) {
			if s.method != "PUT" || !strings.HasPrefix(s.path, "/_matrix/client/v3/rooms/!room:example.org/send/m.room.message/fstui-") || s.auth != "Bearer tok" {
				t.Errorf("matrix %s %s %q", s.method, s.path, s.auth)
			}
		}, ""},
	} {
		t.Run(c.typ, func(t *testing.T) {
			var got seen
			ch := server(t, c.typ, recording(t, &got, http.StatusOK))
			c.setup(&ch)
			if err := Send(t.Context(), ch, m); err != nil {
				t.Fatal(err)
			}
			c.check(t, got)
		})
	}
}

func TestMissingFields(t *testing.T) {
	for _, ch := range []Channel{
		{Type: "gotify", URL: "https://g"},
		{Type: "pushover", Secret: "a"},
		{Type: "telegram", Secret: "a"},
		{Type: "matrix", URL: "https://m", Secret: "a"},
		{Type: "smtp", Host: "h", Port: 25, From: "a@b"},
	} {
		if err := Send(t.Context(), ch, Message{}); err == nil || !strings.Contains(err.Error(), "missing") {
			t.Errorf("%s: %v", ch.Type, err)
		}
	}
}

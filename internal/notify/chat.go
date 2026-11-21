// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// errMissing reports a field a channel type needs.
var errMissing = errors.New("missing")

// jsonRequest builds a request with a JSON body.
func jsonRequest(method string, u *url.URL, v any) (request, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return request{}, fmt.Errorf("encoding: %w", err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return request{method: method, url: u, header: h, body: b}, nil
}

// text is the title and body as one plain text, for services without a
// title of their own.
func text(m Message) string {
	if m.Body == "" {
		return m.Title
	}
	return m.Title + "\n" + m.Body
}

// apiURL is ch.URL when set, else the service's own address.
func apiURL(ch Channel, official string) (*url.URL, error) {
	if ch.URL == "" {
		ch.URL = official
	}
	return checkURL(ch, ch.URL)
}

// sendGotify posts to a Gotify server's /message with an application
// token.
func sendGotify(ctx context.Context, ch Channel, m Message) error {
	u, err := checkURL(ch, ch.URL)
	if err != nil {
		return err
	}
	if ch.Secret == "" {
		return fmt.Errorf("application token %w", errMissing)
	}
	r, err := jsonRequest(http.MethodPost, u.JoinPath("message"), map[string]any{
		"title": m.Title, "message": m.Body, "priority": map[Level]int{Info: 2, Warning: 5, Error: 8}[m.Level],
	})
	if err != nil {
		return err
	}
	r.header.Set("X-Gotify-Key", ch.Secret)
	return post(ctx, ch, r)
}

// sendPushover posts to Pushover's messages API (pushover.net/api).
func sendPushover(ctx context.Context, ch Channel, m Message) error {
	u, err := apiURL(ch, "https://api.pushover.net/1/messages.json")
	if err != nil {
		return err
	}
	if ch.Secret == "" || ch.UserKey == "" {
		return fmt.Errorf("application token or user key %w", errMissing)
	}
	form := url.Values{
		"token": {ch.Secret}, "user": {ch.UserKey}, "title": {m.Title}, "message": {m.Body},
		"priority": {strconv.Itoa(map[Level]int{Info: -1, Warning: 0, Error: 1}[m.Level])},
	}
	h := http.Header{}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	return post(ctx, ch, request{method: http.MethodPost, url: u, header: h, body: []byte(form.Encode())})
}

// sendTelegram sends a plain text message through a bot. Without a
// parse mode, Telegram shows the text as it is: nothing in it is markup.
func sendTelegram(ctx context.Context, ch Channel, m Message) error {
	if ch.Secret == "" || ch.ChatID == "" {
		return fmt.Errorf("bot token or chat ID %w", errMissing)
	}
	u, err := apiURL(ch, "https://api.telegram.org")
	if err != nil {
		return err
	}
	r, err := jsonRequest(http.MethodPost, u.JoinPath(url.PathEscape("bot"+ch.Secret), "sendMessage"), map[string]any{"chat_id": ch.ChatID, "text": text(m)})
	if err != nil {
		return err
	}
	return post(ctx, ch, r)
}

// discordLimit is the length of a Discord message's content.
const discordLimit = 2000

// sendDiscord posts to a Discord webhook, whose URL is the secret.
// Mentions are switched off, so text from ZFS cannot ping anyone.
func sendDiscord(ctx context.Context, ch Channel, m Message) error {
	u, err := checkURL(ch, ch.Secret)
	if err != nil {
		return fmt.Errorf("webhook URL: %w", err)
	}
	content := text(m)
	if r := []rune(content); len(r) > discordLimit {
		content = string(r[:discordLimit-1]) + "…"
	}
	r, err := jsonRequest(http.MethodPost, u, map[string]any{"content": content, "allowed_mentions": map[string]any{"parse": []string{}}})
	if err != nil {
		return err
	}
	return post(ctx, ch, r)
}

// slackEscape escapes the three characters Slack reads as markup, so text
// from ZFS cannot form a link or a mention such as <!channel>.
var slackEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// sendSlack posts to a Slack incoming webhook, whose URL is the secret.
func sendSlack(ctx context.Context, ch Channel, m Message) error {
	u, err := checkURL(ch, ch.Secret)
	if err != nil {
		return fmt.Errorf("webhook URL: %w", err)
	}
	r, err := jsonRequest(http.MethodPost, u, map[string]any{"text": slackEscape.Replace(text(m))})
	if err != nil {
		return err
	}
	return post(ctx, ch, r)
}

// sendMatrix sends a text message to a room with an access token, as the
// client-server API's PUT /rooms/{room}/send/m.room.message/{txn}.
func sendMatrix(ctx context.Context, ch Channel, m Message) error {
	u, err := checkURL(ch, ch.URL)
	if err != nil {
		return err
	}
	if ch.Secret == "" || ch.Room == "" {
		return fmt.Errorf("access token or room %w", errMissing)
	}
	var txn [12]byte
	if _, err := rand.Read(txn[:]); err != nil {
		return fmt.Errorf("transaction ID: %w", err)
	}
	u = u.JoinPath("_matrix", "client", "v3", "rooms", url.PathEscape(ch.Room), "send", "m.room.message", "fstui-"+hex.EncodeToString(txn[:]))
	r, err := jsonRequest(http.MethodPut, u, map[string]any{"msgtype": "m.text", "body": text(m)})
	if err != nil {
		return err
	}
	r.header.Set("Authorization", "Bearer "+ch.Secret)
	return post(ctx, ch, r)
}

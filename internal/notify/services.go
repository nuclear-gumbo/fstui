// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// sendNtfy publishes to an ntfy topic: the body is the message, the title
// and priority are headers (docs.ntfy.sh/publish).
func sendNtfy(ctx context.Context, ch Channel, m Message) error {
	u, err := checkURL(ch, ch.URL)
	if err != nil {
		return err
	}
	if ch.Topic == "" {
		return fmt.Errorf("no topic")
	}
	u = u.JoinPath(url.PathEscape(ch.Topic))
	h := http.Header{}
	h.Set("X-Title", headerSafe(m.Title))
	h.Set("X-Priority", strconv.Itoa(map[Level]int{Info: 2, Warning: 4, Error: 5}[m.Level]))
	h.Set("X-Tags", map[Level]string{Info: "information_source", Warning: "warning", Error: "rotating_light"}[m.Level])
	h.Set("Content-Type", "text/plain; charset=utf-8")
	authorize(h, ch)
	return post(ctx, ch, request{method: http.MethodPost, url: u, header: h, body: []byte(m.Body)})
}

// webhookBody is what a webhook receives, as JSON.
type webhookBody struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Level   string `json:"level"`
}

// sendWebhook posts the message as JSON to any URL.
func sendWebhook(ctx context.Context, ch Channel, m Message) error {
	u, err := checkURL(ch, ch.URL)
	if err != nil {
		return err
	}
	body, err := json.Marshal(webhookBody{Title: m.Title, Message: m.Body, Level: [...]string{"info", "warning", "error"}[m.Level]})
	if err != nil {
		return fmt.Errorf("encoding: %w", err)
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	authorize(h, ch)
	return post(ctx, ch, request{method: http.MethodPost, url: u, header: h, body: body})
}

// authorize adds basic authentication with User, or Secret as a bearer
// token without one.
func authorize(h http.Header, ch Channel) {
	switch {
	case ch.User != "":
		r := http.Request{Header: h}
		r.SetBasicAuth(ch.User, ch.Secret)
	case ch.Secret != "":
		h.Set("Authorization", "Bearer "+ch.Secret)
	}
}

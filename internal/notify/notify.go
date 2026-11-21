// SPDX-License-Identifier: GPL-3.0-or-later

// Package notify sends notifications. It is the only package in fstui that
// opens network connections (depguard enforces it): over HTTPS to the
// services a channel names, and SMTP to a mail server. It uses the
// standard library only.
package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// Level is how serious a message is; services that have priorities get
// one from it.
type Level int

// Levels.
const (
	Info Level = iota
	Warning
	Error
)

// Message is what a channel sends: a one-line title and a body. Text from
// ZFS may be in either; senders encode it for their request and never
// interpret it.
type Message struct {
	Title string
	Body  string
	Level Level
}

// Channel is one configured destination. Which fields a type uses is in
// docs/configuration.md; the secret is a password, token or, for Discord
// and Slack, the webhook URL itself.
type Channel struct {
	Name string
	Type string // ntfy, webhook, gotify, pushover, telegram, discord, slack, matrix, smtp
	// URL is the server (ntfy, webhook, gotify, matrix), or replaces the
	// service's API address (pushover, telegram), for a relay or a test.
	URL string
	// Topic is ntfy's topic; ChatID Telegram's chat; Room a Matrix room
	// ID; UserKey a Pushover user or group key.
	Topic, ChatID, Room, UserKey string
	// SMTP: the server, port (465 is TLS from the start; any other port
	// must offer STARTTLS), sender and recipients.
	Host string
	Port int
	From string
	To   []string
	// User, with Secret as the password, is HTTP basic authentication;
	// without User, Secret is a bearer token.
	User   string
	Secret string
	// CAFile adds a certificate authority, for a server with a private
	// certificate. There is no option to skip verification.
	CAFile string
	// InsecureHTTP allows an http:// URL.
	InsecureHTTP bool
	// AttemptTimeout bounds one try, SendTimeout all of them; Retries is
	// how many times a send that may succeed later is tried again. Zero
	// timeouts are the defaults; see FromConfig.
	AttemptTimeout, SendTimeout time.Duration
	Retries                     int
}

// ErrUnknownType is returned for a channel type fstui does not send to.
var ErrUnknownType = errors.New("unknown channel type")

// Send sends m to ch, retrying where that can help (see post).
func Send(ctx context.Context, ch Channel, m Message) error {
	var err error
	switch ch.Type {
	case "ntfy":
		err = sendNtfy(ctx, ch, m)
	case "webhook":
		err = sendWebhook(ctx, ch, m)
	case "gotify":
		err = sendGotify(ctx, ch, m)
	case "pushover":
		err = sendPushover(ctx, ch, m)
	case "telegram":
		err = sendTelegram(ctx, ch, m)
	case "discord":
		err = sendDiscord(ctx, ch, m)
	case "slack":
		err = sendSlack(ctx, ch, m)
	case "matrix":
		err = sendMatrix(ctx, ch, m)
	case "smtp":
		err = sendSMTP(ctx, ch, m)
	case "heartbeat":
		err = sendHeartbeat(ctx, ch)
	default:
		err = fmt.Errorf("%w %q", ErrUnknownType, ch.Type)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", ch.Name, err)
	}
	return nil
}

// headerSafe makes s fit an HTTP header or a mail subject: control
// characters, CR and LF among them, become spaces, so text from ZFS cannot
// add a header of its own.
func headerSafe(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
}

// needsSecret are the channel types that cannot send without one.
var needsSecret = []string{"gotify", "pushover", "telegram", "discord", "slack", "matrix"}

// NeedsSecret reports whether ch cannot send without a secret: its type
// needs one, or it names a user to log in as.
func NeedsSecret(ch config.Channel) bool {
	return slices.Contains(needsSecret, ch.Type) || ch.User != ""
}

// FromConfig is the channel to send to for a configured one, its secret,
// and the timing in the notifications: settings.
func FromConfig(ch config.Channel, secret string, n config.Notifications) Channel {
	return Channel{
		Name: ch.Name, Type: ch.Type, URL: ch.URL, Topic: ch.Topic, ChatID: ch.ChatID, Room: ch.Room, UserKey: ch.UserKey,
		Host: ch.Host, Port: ch.Port, From: ch.From, To: ch.To, User: ch.User, Secret: secret, CAFile: ch.CAFile, InsecureHTTP: ch.InsecureHTTP,
		AttemptTimeout: n.AttemptTimeout, SendTimeout: n.SendTimeout, Retries: n.Retries,
	}
}

// ErrNoSecret is returned by Test for a channel that needs a secret and
// has none.
var ErrNoSecret = errors.New("no secret: add the channel to secrets.yaml, or set secret_file")

// Test sends a test message to ch, with its secret from secrets and the
// timing in n.
func Test(ctx context.Context, n config.Notifications, ch config.Channel, secrets config.Secrets, host string) error {
	secret, err := secrets.Secret(ch)
	switch {
	case err != nil && !errors.Is(err, config.ErrNoSecret):
		return fmt.Errorf("%s: %w", ch.Name, err)
	case err != nil && NeedsSecret(ch):
		return fmt.Errorf("%s: %w", ch.Name, ErrNoSecret)
	}
	return Send(ctx, FromConfig(ch, secret, n), Message{Title: host + ": fstui test", Body: "A test message from fstui to the channel " + ch.Name + ".", Level: Info})
}

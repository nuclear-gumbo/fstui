// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// channelForm edits one channel: its settings by type, and its secret,
// which is typed masked and never shown again. It is a fieldForm, so it
// works as every form in fstui does.
type channelForm struct {
	fieldForm
	editing string // the channel's name before editing; "" for a new one
}

// typeFields are the settings each type needs, after name and type.
var typeFields = map[string][]formField{
	"ntfy": {
		{key: "url", label: "Server", hint: "https://ntfy.sh, or your own server"},
		{key: "topic", label: "Topic"},
		{key: "user", label: "User", hint: "With a user, the secret is its password; without one, an access token or nothing"},
	},
	"webhook":  {{key: "url", label: "URL", hint: `Receives a POST with {"title", "message", "level"} as JSON`}},
	"gotify":   {{key: "url", label: "Server"}},
	"pushover": {{key: "user_key", label: "User key", hint: "Your Pushover user or group key"}},
	"telegram": {{key: "chat_id", label: "Chat ID", hint: "A number such as -1001234, or @channel"}},
	"matrix":   {{key: "url", label: "Homeserver"}, {key: "room", label: "Room", hint: "The room ID, such as !abc:example.org"}},
	"smtp": {
		{key: "host", label: "Server"},
		{key: "port", label: "Port", hint: "465 is TLS from the start; any other port must offer STARTTLS"},
		{key: "from", label: "From"},
		{key: "to", label: "To", hint: "One address, or several separated by commas"},
		{key: "user", label: "User", hint: "Empty: send without logging in"},
	},
}

// secretLabel names a type's secret.
var secretLabel = map[string]string{
	"ntfy": "Password or token", "webhook": "Bearer token (optional)", "gotify": "Application token", "pushover": "Application token",
	"telegram": "Bot token", "discord": "Webhook URL", "slack": "Webhook URL", "matrix": "Access token", "smtp": "Password",
}

func newChannelForm(ch config.Channel) *channelForm {
	v := map[string]string{
		"name": ch.Name, "type": cmp.Or(ch.Type, "ntfy"), "url": ch.URL, "topic": ch.Topic, "chat_id": ch.ChatID, "room": ch.Room,
		"user_key": ch.UserKey, "host": ch.Host, "from": ch.From, "to": strings.Join(ch.To, ", "), "user": ch.User,
		"ca_file": ch.CAFile, "min_severity": cmp.Or(ch.MinSeverity, "warning"), "insecure_http": strconv.FormatBool(ch.InsecureHTTP),
	}
	if ch.Port != 0 {
		v["port"] = strconv.Itoa(ch.Port)
	}
	title := "New channel"
	if ch.Name != "" {
		title = "Channel " + ch.Name
	}
	return &channelForm{fieldForm: fieldForm{title: title, values: v, fieldsOf: channelFields}, editing: ch.Name}
}

// channelFields are the form's rows for the chosen type, in three
// groups: the channel, how to reach it, and the options most leave alone.
func channelFields(v map[string]string) []formField {
	head := []formField{{key: "name", label: "Name", hint: "Lower-case letters, digits, - and _: phone, mail"}, {key: "type", label: "Type", choices: config.ChannelTypes}}
	conn := append(slices.Clone(typeFields[v["type"]]),
		formField{key: "secret", label: secretLabel[v["type"]], secret: true, hint: "Typed hidden; stored in secrets.yaml (mode 0600), never shown again"})
	opts := []formField{
		{key: "min_severity", label: "Least severity", choices: config.ProblemSeverities, hint: "The least serious alert this channel is sent"},
		{key: "ca_file", label: "CA file", placeholder: "none", hint: "A PEM file, for a server with a private certificate"},
		{key: "insecure_http", label: "Allow http://", choices: []string{"false", "true"}, hint: "Plain HTTP sends messages, and any token, unencrypted"},
	}
	var out []formField
	for _, g := range []struct {
		name   string
		fields []formField
	}{{"Channel", head}, {"How to reach it", conn}, {"Options", opts}} {
		for _, f := range g.fields {
			f.group = g.name
			out = append(out, f)
		}
	}
	return out
}

// channel is the form as a channel to write.
func (f *channelForm) channel() config.Channel {
	v := f.values
	ch := config.Channel{Name: strings.TrimSpace(v["name"]), Type: v["type"], MinSeverity: v["min_severity"], CAFile: v["ca_file"], InsecureHTTP: v["insecure_http"] == "true"}
	for _, fl := range typeFields[ch.Type] {
		s := strings.TrimSpace(v[fl.key])
		switch fl.key {
		case "url":
			ch.URL = s
		case "topic":
			ch.Topic = s
		case "user":
			ch.User = s
		case "user_key":
			ch.UserKey = s
		case "chat_id":
			ch.ChatID = s
		case "room":
			ch.Room = s
		case "host":
			ch.Host = s
		case "port":
			ch.Port, _ = strconv.Atoi(s) // checked when the file is reviewed
		case "from":
			ch.From = s
		case "to":
			for _, a := range strings.Split(s, ",") {
				if a = strings.TrimSpace(a); a != "" {
					ch.To = append(ch.To, a)
				}
			}
		}
	}
	return ch
}

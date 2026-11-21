// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
)

// Notifications is the notifications: section: where fstui check sends
// pool problems and when (docs/configuration.md, Notifications).
type Notifications struct {
	// Repeat is how long a problem waits before it is sent again; 0 never
	// repeats.
	Repeat time.Duration
	// Resolved sends a message when a problem clears.
	Resolved bool
	// Events is whether kernel events are sent: "auto" leaves them to ZED
	// when ZED has a channel of its own, "on", or "off".
	Events string
	// EventsMin is the least severe kernel event sent, from SeverityNames.
	EventsMin string
	Channels  []Channel
	// Host names this system in messages; empty is its host name.
	Host string
	// AttemptTimeout bounds one try at sending, SendTimeout all the tries
	// of one message, Retries how many times a failed send is tried again.
	AttemptTimeout, SendTimeout time.Duration
	Retries                     int
	// CheckTimeout bounds one fstui check.
	CheckTimeout time.Duration
	// WatchInterval is how often fstui watch checks; WatchEvents how
	// often it looks for new kernel events between checks (0: never).
	WatchInterval, WatchEvents time.Duration
	// HeartbeatURL is requested after every check that read the pools,
	// so a service outside the host notices when the checks stop.
	HeartbeatURL string
	// Templates are the user's own alert templates; Alerts turn templates
	// on (alerts.go). No alerts: ActiveAlerts gives the defaults.
	Templates []Template
	Alerts    []Alert
}

// Channel is one destination. Its secret is in secrets.yaml under its
// name, or in SecretFile; never here.
type Channel struct {
	Name, Type string
	URL        string
	Topic      string // ntfy
	ChatID     string // telegram
	Room       string // matrix
	UserKey    string // pushover
	Host       string // smtp
	Port       int    // smtp
	From       string // smtp
	To         []string
	User       string
	CAFile     string
	SecretFile string
	// InsecureHTTP allows an http:// URL.
	InsecureHTTP bool
	// MinSeverity is the least severe problem sent: info, warning, error.
	MinSeverity string
}

// ChannelTypes are the types a channel may have.
var ChannelTypes = []string{"ntfy", "webhook", "gotify", "pushover", "telegram", "discord", "slack", "matrix", "smtp"}

// ProblemSeverities are what a channel's min_severity may be.
var ProblemSeverities = []string{"info", "warning", "error"}

// required lists the settings each channel type cannot do without; the
// secret is checked when it is read (secrets.yaml).
var required = map[string][]string{
	"ntfy": {"url", "topic"}, "webhook": {"url"}, "gotify": {"url"}, "pushover": {"user_key"},
	"telegram": {"chat_id"}, "discord": {}, "slack": {}, "matrix": {"url", "room"}, "smtp": {"host", "port", "from", "to"},
}

// maxRepeat bounds notifications.repeat.
const maxRepeat = 30 * 24 * time.Hour

const (
	defaultMinSeverity = "warning"
	defaultRepeat      = 24 * time.Hour
	channelsMax        = 32
	portMax            = 65535
)

// channelName keeps names usable as keys in secrets.yaml and on a command
// line (fstui check --test NAME).
var channelName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func defaultNotifications() Notifications {
	return Notifications{
		Repeat: defaultRepeat, Resolved: true, Events: "auto", EventsMin: "error",
		AttemptTimeout: 10 * time.Second, SendTimeout: 30 * time.Second, Retries: 3, CheckTimeout: 5 * time.Minute,
		WatchInterval: 5 * time.Minute, WatchEvents: 15 * time.Second,
	}
}

// setChannels reads the list of channels. A channel with a problem is
// reported and left out; the others apply. A file that sets channels
// replaces the channels of the files before it.
func setChannels(a *applier, c *Config, n *yaml.Node) error {
	out, err := namedList(a, n, "notifications.channels", "channel", channelsMax, channelOf, func(ch Channel) string { return ch.Name })
	c.Notifications.Channels = out
	return err
}

// namedList reads a list of named items with read. An item with a
// problem, or a name already listed, is reported and left out.
func namedList[T any](a *applier, n *yaml.Node, path, what string, limit int, read func(*yaml.Node) (T, error), name func(T) string) ([]T, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("want a list of %ss, each with a name", what)
	}
	if len(n.Content) > limit {
		return nil, fmt.Errorf("at most %d %ss", limit, what)
	}
	var out []T
	for i, item := range n.Content {
		v, err := read(item)
		if err == nil && slices.ContainsFunc(out, func(o T) bool { return name(o) == name(v) }) {
			err = fmt.Errorf("a %s named %q is already listed", what, name(v))
		}
		if err != nil {
			a.report(item, "%s[%d]: %v", path, i, err)
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// channelOf reads one channel and checks it is complete for its type.
func channelOf(n *yaml.Node) (Channel, error) {
	ch := Channel{MinSeverity: defaultMinSeverity}
	if n.Kind != yaml.MappingNode {
		return ch, fmt.Errorf("want a mapping with name, type and the type's settings")
	}
	set := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if err := channelField(&ch, k.Value, v); err != nil {
			return ch, fmt.Errorf("%s: %w", k.Value, err)
		}
		set[k.Value] = true
	}
	switch {
	case !channelName.MatchString(ch.Name):
		return ch, fmt.Errorf("name: want 1 to 40 lower-case letters, digits, - and _")
	case !slices.Contains(ChannelTypes, ch.Type):
		return ch, fmt.Errorf("type: want one of %v", ChannelTypes)
	}
	for _, r := range required[ch.Type] {
		if !set[r] {
			return ch, fmt.Errorf("a %s channel needs %s", ch.Type, r)
		}
	}
	if ch.URL != "" {
		if err := checkURL(ch.URL, ch.InsecureHTTP); err != nil {
			return ch, fmt.Errorf("url: %w", err)
		}
	}
	return ch, nil
}

// channelField sets one setting of a channel.
func channelField(ch *Channel, key string, v *yaml.Node) error {
	str := func(dst *string, want string) error {
		s, err := scalar(v, "!!str", want)
		*dst = s
		return err
	}
	switch key {
	case "name":
		return str(&ch.Name, "a name")
	case "type":
		return str(&ch.Type, "a channel type")
	case "url":
		return str(&ch.URL, "a URL")
	case "topic":
		return str(&ch.Topic, "an ntfy topic")
	case "chat_id":
		return chatID(ch, v)
	case "room":
		return str(&ch.Room, "a Matrix room ID such as !abc:example.org")
	case "user_key":
		return str(&ch.UserKey, "a Pushover user or group key")
	case "host":
		return str(&ch.Host, "a mail server")
	case "port":
		p, err := intIn(v, 1, portMax)
		ch.Port = p
		return err
	case "from":
		return str(&ch.From, "a mail address")
	case "to":
		return recipients(ch, v)
	case "user":
		return str(&ch.User, "a user name")
	case "ca_file":
		return str(&ch.CAFile, "the path of a PEM certificate file")
	case "secret_file":
		return str(&ch.SecretFile, "the path of a file holding the secret")
	case "insecure_http":
		return boolean(v, &ch.InsecureHTTP)
	case "min_severity":
		s, err := oneOf(v, ProblemSeverities...)
		ch.MinSeverity = s
		return err
	}
	return fmt.Errorf("unknown setting")
}

// chatID reads a Telegram chat ID, a number or an @channel name.
func chatID(ch *Channel, v *yaml.Node) error {
	if v.Kind == yaml.ScalarNode && (v.ShortTag() == "!!int" || v.ShortTag() == "!!str") && v.Value != "" {
		ch.ChatID = v.Value
		return nil
	}
	return fmt.Errorf("want a chat ID such as -1001234 or @channel")
}

// recipients reads one address or a list of them.
func recipients(ch *Channel, v *yaml.Node) error {
	items := []*yaml.Node{v}
	if v.Kind == yaml.SequenceNode {
		items = v.Content
	}
	for _, it := range items {
		s, err := scalar(it, "!!str", "a mail address")
		if err != nil {
			return err
		}
		ch.To = append(ch.To, s)
	}
	if len(ch.To) == 0 {
		return fmt.Errorf("want at least one address")
	}
	return nil
}

// checkURL refuses a URL fstui would not send to: not https://, unless
// insecure_http allows http://.
func checkURL(raw string, insecure bool) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return fmt.Errorf("%w", err)
	case u.Host == "":
		return fmt.Errorf("no host in %q", raw)
	case u.Scheme == "https", u.Scheme == "http" && insecure:
		return nil
	case u.Scheme == "http":
		return fmt.Errorf("http:// needs insecure_http: true on the channel")
	}
	return fmt.Errorf("want an https:// URL, found %s", strconv.Quote(u.Scheme+"://"))
}

// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/format"
	"github.com/nuclear-gumbo/fstui/internal/schedule"
	"github.com/nuclear-gumbo/fstui/internal/theme"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
)

// OpenChangesMsg opens the change menu on the active view's selection, as
// the a key does: the Notifications tab sends it after selecting fstui
// watch in the Schedules tab.
type OpenChangesMsg struct{}

// sending is what delivers notifications on this system: the fstui watch
// service, a timer or cron job running fstui check, or nothing. Storage
// monitors (ZED, smartd, mdadm, NUT) send from something that is always
// running; a user who adds a channel must see whether anything will use
// it.
type sending struct {
	known bool   // the schedules were read
	on    bool   // something runs the checks
	how   string // "fstui watch runs as a service (systemd unit fstui-watch.service)"
	when  string // "Pools are checked every 5m; kernel events go out within 15s."
	why   string // when not known: why the schedules cannot be read
}

// deliveryOf reads the schedules: fstui watch first, as it also follows
// kernel events, then fstui check.
func deliveryOf(s *Schedules, n config.Notifications) sending {
	switch {
	case s == nil || s.read == nil:
		return sending{why: "fstui cannot read this system's schedules in this build"}
	case s.err != "":
		return sending{why: "the schedules could not be read: " + s.err}
	case !s.loaded:
		return sending{}
	}
	out := sending{known: true}
	if i := slices.IndexFunc(s.jobs, func(j schedule.Job) bool { return j.ID == schedule.WatchID }); i >= 0 && s.jobs[i].When == schedule.On {
		out.on = true
		out.how = "fstui watch runs as a service (" + s.jobs[i].By + ")."
		out.when = fmt.Sprintf("Pools are checked every %s; new kernel events go out within %s.", format.Age(n.WatchInterval), format.Age(n.WatchEvents))
		return out
	}
	if i := slices.IndexFunc(s.jobs, func(j schedule.Job) bool { return j.ID == schedule.CheckID }); i >= 0 && s.jobs[i].When != schedule.Off {
		out.on = true
		out.how = "fstui check runs " + s.jobs[i].When + " (" + s.jobs[i].By + ")."
		out.when = "Pools are checked then. fstui watch, as a service, also sends kernel events as they happen."
	}
	return out
}

// setup is the box at the top of the Notifications tab: the three steps
// to being notified, in the order they are done (Grafana: "First, create
// the contact point and test the notifications"), each marked done or not.
func (n *Notifications) setup(d Display, width int) []string {
	pal := d.Palette
	mark := func(done bool) string {
		if done {
			return pal.Style(theme.OK).Render("[x]")
		}
		return pal.Style(theme.Error).Render("[ ]")
	}
	chs := len(n.channels())
	where := "Where to send: no channel yet; " + keyName(keys.Actions) + " adds one."
	if chs > 0 {
		where = fmt.Sprintf("Where to send: %d %s below; %s sends a test message.", chs, format.Plural(chs, "channel", "channels"), keyName(keys.TestChannel))
	}
	on := len(n.cfg.ActiveAlerts())
	what := fmt.Sprintf("What to send: %d %s on, in the Alerts tab.", on, format.Plural(on, "alert", "alerts"))
	del := deliveryOf(n.sched, n.cfg.Notifications)
	lines := []string{
		mark(chs > 0) + " " + where,
		mark(on > 0) + " " + what,
	}
	setUp := keyName(keys.Delivery)
	switch {
	case del.on:
		lines = append(lines, mark(true)+" Sending: "+del.how, "    "+del.when)
	case del.known:
		lines = append(lines,
			mark(false)+" Sending: nothing sends these yet.",
			"    No service or timer on this system runs fstui watch or fstui check.",
			"    "+setUp+" sets up fstui watch as a service (in write mode, as root).")
	case del.why != "":
		lines = append(lines, mark(false)+" Sending: unknown; "+del.why+".",
			"    Run fstui watch as a service, or fstui check from a timer or cron job.")
	default:
		lines = append(lines, "... Sending: reading this system's schedules.")
	}
	lines = append(lines, "", heartbeatLine(n.cfg.Notifications.HeartbeatURL))
	var wrapped []string
	for _, l := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(l, width-3, ""), "\n")...)
	}
	return Box(d, "Getting notified", wrapped, width)
}

// deliveryNote is the line under the channels: what a channel is for, in
// words a first-time reader follows.
func deliveryNote(width int) string {
	return ansi.Wrap(strings.Join([]string{
		"A channel is where messages go: a phone app such as ntfy, chat, or mail.",
		"Each channel gets the alerts at or above its least severity.",
	}, " "), width, "")
}

// heartbeatLine says whether a service outside this host hears from it:
// nothing on the host can report the host itself going down
// (Healthchecks' dead man's switch).
func heartbeatLine(raw string) string {
	if raw == "" {
		return "If this host goes down, it cannot say so. A heartbeat URL (General, Notifications) lets a service such as Healthchecks warn you."
	}
	host := "a service outside this host"
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host = u.Host // the path identifies the monitor; it is not shown
	}
	return "Heartbeat: after every check, " + host + " hears from this host, and warns you when it stops."
}

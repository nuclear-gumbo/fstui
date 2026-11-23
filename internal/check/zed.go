// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// ZEDConfig is OpenZFS's ZFS Event Daemon configuration.
const ZEDConfig = "/etc/zfs/zed.d/zed.rc"

// ZED is what ZED does with kernel events on this system: whether it
// runs, and the channels its zed.rc sets.
type ZED struct {
	Running  bool
	Channels []string
}

// zedChannels are the zed.rc settings that make ZED notify, and their
// names. ZED_EMAIL_ADDR="root" is Debian's default, mail to root on this
// host, and does not count: often nothing reads it.
var zedChannels = []struct{ key, name string }{
	{"ZED_EMAIL_ADDR", "email"},
	{"ZED_NTFY_TOPIC", "ntfy"},
	{"ZED_GOTIFY_URL", "Gotify"},
	{"ZED_PUSHOVER_TOKEN", "Pushover"},
	{"ZED_PUSHBULLET_ACCESS_TOKEN", "Pushbullet"},
	{"ZED_SLACK_WEBHOOK_URL", "Slack"},
}

// DetectZED looks for a running zed process in /proc and reads the
// channels set in rc. It runs nothing.
func DetectZED(proc, rc string) ZED {
	running := false
	comms, _ := filepath.Glob(filepath.Join(proc, "[0-9]*", "comm"))
	for _, c := range comms {
		if b, err := os.ReadFile(c); err == nil && string(bytes.TrimSpace(b)) == "zed" { //nolint:gosec // /proc
			running = true
			break
		}
	}
	b, _ := os.ReadFile(rc) //nolint:gosec // OpenZFS's configuration; missing means no channels
	return ZEDFrom(running, b)
}

// ZEDFrom is what ZED does, from whether it runs and its zed.rc: for a
// remote host, both read over ssh.
func ZEDFrom(running bool, rc []byte) ZED {
	z := ZED{Running: running}
	set := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(rc))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			set[k] = strings.Trim(v, `"'`)
		}
	}
	for _, c := range zedChannels {
		if v := set[c.key]; v != "" && (c.key != "ZED_EMAIL_ADDR" || v != "root") {
			z.Channels = append(z.Channels, c.name)
		}
	}
	return z
}

// SendsEvents reports whether ZED tells someone about kernel events.
func (z ZED) SendsEvents() bool { return z.Running && len(z.Channels) > 0 }

// Describe says what ZED does with kernel events, for the Notifications
// tab.
func (z ZED) Describe() string {
	switch {
	case z.SendsEvents():
		return "ZED is running and sends kernel events by " + strings.Join(z.Channels, ", ") + "; under events: auto, fstui leaves them to it."
	case z.Running:
		return "ZED is running but sends no notifications (" + ZEDConfig + "); fstui sends kernel events under events: auto."
	}
	return "ZED is not running; fstui sends kernel events under events: auto or on."
}

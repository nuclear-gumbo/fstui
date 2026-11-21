// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package schedule

import "strings"

// hardening confines fstui check and watch to what they need: they read
// pools and send messages, and write only their state directory.
const hardening = `NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=yes
StateDirectory=fstui
StateDirectoryMode=0700
`

// checkUnits are fstui-check.service and fstui-check.timer.
func checkUnits(binary, every string) (service, timer string) {
	service = `# Written by fstui: change it in the Schedules tab of the Config view.
[Unit]
Description=fstui check: look at every pool and send notifications
After=local-fs.target

[Service]
Type=oneshot
ExecStart=` + systemdQuote(binary) + ` check
` + hardening
	timer = `# Written by fstui: change it in the Schedules tab of the Config view.
[Unit]
Description=Run fstui check every ` + every + `

[Timer]
OnBootSec=5m
OnUnitActiveSec=` + every + `

[Install]
WantedBy=timers.target
`
	return service, timer
}

// watchUnit is fstui-watch.service.
func watchUnit(binary string) string {
	return `# Written by fstui: change it in the Schedules tab of the Config view.
[Unit]
Description=fstui watch: notifications as pools change
After=local-fs.target

[Service]
ExecStart=` + systemdQuote(binary) + ` watch
Restart=on-failure
RestartSec=30s
` + hardening + `
[Install]
WantedBy=multi-user.target
`
}

// openRCScript is /etc/init.d/fstui-watch, supervised so it restarts.
func openRCScript(binary string) string {
	return `#!/sbin/openrc-run
# Written by fstui: change it in the Schedules tab of the Config view.
description="fstui watch: notifications as pools change"
supervisor=supervise-daemon
command=` + shellQuote(binary) + `
command_args="watch"
respawn_delay=30
output_log="/var/log/fstui-watch.log"
error_log="/var/log/fstui-watch.log"

depend() {
	need localmount
	use net
}
`
}

// systemdQuote quotes a path for ExecStart when it needs it.
func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\$%;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

// shellQuote quotes a word for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

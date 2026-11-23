// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/check"
	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/statedir"
)

const checkUsage = `Usage: fstui check [options]

Look at every pool once, send the notifications the configuration calls
for (notifications: in config.yaml), print what was found, and exit. It
changes no pool. Exit status 1 if a channel failed.
`

// runCheck is fstui check.
func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("fstui check", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	test := fs.String("test", "", "send a test message to the channel `NAME`, then exit")
	fs.BoolP("help", "h", false, "print this help, then exit")
	switch err := fs.Parse(args); {
	case errors.Is(err, pflag.ErrHelp):
		return report(stdout, 0, "%s\nOptions:\n%s", checkUsage, optionList(fs))
	case err != nil:
		return report(stderr, exitUsage, "fstui check: %v\n", err)
	case fs.NArg() > 0:
		return report(stderr, exitUsage, "fstui check: unexpected argument %q\n", fs.Arg(0))
	}
	if help, _ := fs.GetBool("help"); help {
		return report(stdout, 0, "%s\nOptions:\n%s", checkUsage, optionList(fs))
	}
	runs, notices, err := checkRuns()
	for _, n := range notices {
		_, _ = fmt.Fprintf(stderr, "fstui check: %s\n", n) // a failed write shows in the status of the report below
	}
	if err != nil {
		return report(stderr, exitFailed, "fstui check: %v\n", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runs[0].Config.Notifications.CheckTimeout)
	defer cancel()
	if *test != "" {
		if err := runs[0].Test(ctx, *test); err != nil {
			return report(stderr, exitFailed, "fstui check: %v\n", err)
		}
		return report(stdout, 0, "%s: test message sent\n", *test)
	}
	status := 0
	for _, r := range runs {
		if len(runs) > 1 {
			say(stdout, "%s:\n", r.Host)
		}
		status = max(status, printResult(stdout, r.Once(ctx)))
	}
	return status
}

// bootIDPath is the kernel's boot ID.
const bootIDPath = "/proc/sys/kernel/random/boot_id"

// checkRuns builds the runs of a check, one per host in
// notifications.hosts, this machine first. Their connections to other
// hosts close by themselves once unused (remote.persist).
func checkRuns() (runs []check.Run, notices []string, err error) {
	local, notices, err := checkRun()
	if err != nil {
		return nil, notices, err
	}
	onHere, hosts, problems := local.Config.CheckedHosts()
	notices = append(notices, problems...)
	if onHere {
		runs = append(runs, local)
	}
	remote, more := remoteRuns(local, hosts)
	notices = append(notices, more...)
	runs = append(runs, remote...)
	if len(runs) == 0 {
		return nil, notices, errors.New("notifications.hosts names no host to check")
	}
	for i := 1; i < len(runs); i++ {
		runs[i].NoHeartbeat = true // one request per pass
	}
	return runs, notices, nil
}

// checkRun builds this machine's run from the configuration, the
// secrets and the ZFS backend.
func checkRun() (check.Run, []string, error) {
	var notices []string
	user, err := config.UserFile()
	if err != nil {
		user = ""
	}
	cfg, problems := config.Load(config.Files(user)...)
	secrets, secretProblems := config.LoadSecrets(config.SecretsFiles(user)...)
	for _, p := range append(problems, secretProblems...) {
		notices = append(notices, p.String())
	}
	src, err := checkSource(cfg)
	if err != nil {
		return check.Run{}, notices, err
	}
	dir, err := statedir.Dir()
	if err != nil {
		return check.Run{}, notices, err
	}
	dir = filepath.Join(dir, "check") // private, like saved plans: for a user the state directory also holds the audit log
	send, dir := checkSending(dir)
	host, _ := os.Hostname()
	host = cmp.Or(cfg.Notifications.Host, host)
	boot, _ := os.ReadFile(bootIDPath)
	return check.Run{
		Source: src, Config: cfg, Secrets: secrets, StateDir: dir,
		Host: host, Boot: strings.TrimSpace(string(boot)), ZED: check.DetectZED("/proc", check.ZEDConfig),
		Now: time.Now, Send: send,
	}, notices, nil
}

// printResult prints one line per problem, then what each channel did,
// and returns the exit status.
func printResult(w io.Writer, res check.Result) int {
	var b strings.Builder
	if len(res.Problems) == 0 {
		b.WriteString("No problems.\n")
	}
	for _, p := range res.Problems {
		fmt.Fprintf(&b, "%-7s  %s\n", p.Severity, p.Text)
	}
	if res.EventsNote != "" {
		b.WriteString(res.EventsNote + "\n")
	}
	status := 0
	for _, name := range channelNames(res) {
		switch {
		case res.Failed[name] != nil:
			fmt.Fprintf(&b, "%s: failed: %v\n", name, res.Failed[name])
			status = exitFailed
		case res.Skipped[name] != "":
			fmt.Fprintf(&b, "%s: not used: %s\n", name, res.Skipped[name])
		default:
			fmt.Fprintf(&b, "%s: sent %d\n", name, res.Sent[name])
		}
	}
	if res.Heartbeat != "" {
		fmt.Fprintf(&b, "heartbeat: %s\n", res.Heartbeat)
		if strings.HasPrefix(res.Heartbeat, "failed") {
			status = exitFailed
		}
	}
	if res.StateErr != nil {
		fmt.Fprintf(&b, "%v\n", res.StateErr)
		status = exitFailed
	}
	return report(w, status, "%s", b.String())
}

// channelNames are the channels that had something to send.
func channelNames(res check.Result) []string {
	set := map[string]bool{}
	for k := range res.Sent {
		set[k] = true
	}
	for k := range res.Failed {
		set[k] = true
	}
	for k := range res.Skipped {
		set[k] = true
	}
	return slices.Sorted(maps.Keys(set))
}

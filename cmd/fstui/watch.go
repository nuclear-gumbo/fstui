// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/check"
	"github.com/nuclear-gumbo/fstui/internal/format"
	"github.com/nuclear-gumbo/fstui/internal/statedir"
)

const watchUsage = `Usage: fstui watch [options]

Run fstui check every notifications.watch_interval, and at once when the
kernel reports an event worth sending, until stopped. For a service. The
configuration is read again for every check; SIGHUP checks at once. It
changes no pool. Only one fstui watch runs per user.
`

// runWatch is fstui watch.
func runWatch(args []string, stdout, stderr io.Writer) int {
	fs := pflag.NewFlagSet("fstui watch", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	fs.BoolP("help", "h", false, "print this help, then exit")
	switch err := fs.Parse(args); {
	case errors.Is(err, pflag.ErrHelp):
		return report(stdout, 0, "%s\nOptions:\n%s", watchUsage, optionList(fs))
	case err != nil:
		return report(stderr, exitUsage, "fstui watch: %v\n", err)
	case fs.NArg() > 0:
		return report(stderr, exitUsage, "fstui watch: unexpected argument %q\n", fs.Arg(0))
	}
	if help, _ := fs.GetBool("help"); help {
		return report(stdout, 0, "%s\nOptions:\n%s", watchUsage, optionList(fs))
	}
	r, notices, err := checkRun() // this machine's state directory holds the lock
	if err != nil {
		return report(stderr, exitFailed, "fstui watch: %v\n", err)
	}
	if err := statedir.Private(r.StateDir); err != nil {
		return report(stderr, exitFailed, "fstui watch: %v\n", err)
	}
	lock, err := statedir.TakeLock(filepath.Join(r.StateDir, "watch.lock"))
	if err != nil {
		return report(stderr, exitFailed, "fstui watch: another fstui watch is running: %v\n", err)
	}
	defer lock.Release()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	wake := make(chan struct{}, 1)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for range hup {
			select {
			case wake <- struct{}{}:
			default: // a check is already due
			}
		}
	}()

	n := r.Config.Notifications
	say(stdout, "fstui watch: checking every %s; kernel events looked for every %s\n", format.Age(n.WatchInterval), every(n.WatchEvents))
	printNotices(stderr, notices)
	w, closeRuns := newWatch(stdout, stderr, notices)
	defer closeRuns()
	w.Loop(ctx, wake)
	say(stdout, "fstui watch: stopped\n")
	return 0
}

// newWatch is the loop's view of fstui watch: each pass loads the runs
// of every checked host anew and prints what is worth printing. The
// returned function ends the last pass's connections.
func newWatch(stdout, stderr io.Writer, notices []string) (check.Watch, func()) {
	closeRuns := func() {}
	shown := notices
	return check.Watch{
		Load: func() ([]check.Run, []string, error) {
			closeRuns() // the last pass's connections, before the new runs open theirs on the same sockets
			runs, notices, closer, err := checkRuns()
			closeRuns = closer
			return runs, notices, err
		},
		Report: func(res check.Result, notices []string) {
			if !slices.Equal(notices, shown) { // a file changed: say so once, not every pass
				printNotices(stderr, notices)
				shown = notices
			}
			if worthPrinting(res) {
				say(stdout, "%s:\n", res.Host)
				_ = printResult(stdout, res) // the status is for fstui check; a service keeps going
			}
		},
		Fail: func(err error) { say(stderr, "fstui watch: %v\n", err) },
	}, func() { closeRuns() }
}

// every is a poll interval in words; 0 is off.
func every(d time.Duration) string {
	if d == 0 {
		return "never (notifications.watch_events is 0)"
	}
	return format.Age(d)
}

// worthPrinting: a pass that sent, failed or could not save its state is
// logged; a quiet pass is not, so the journal shows changes only.
func worthPrinting(res check.Result) bool {
	for _, n := range res.Sent {
		if n > 0 {
			return true
		}
	}
	return len(res.Failed) > 0 || res.StateErr != nil
}

func printNotices(w io.Writer, notices []string) {
	for _, n := range notices {
		say(w, "fstui watch: %s\n", n)
	}
}

// say writes a line of the service's log; a failed write has nowhere
// better to go.
func say(w io.Writer, f string, args ...any) { _, _ = fmt.Fprintf(w, f, args...) }

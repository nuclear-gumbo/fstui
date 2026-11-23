// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nuclear-gumbo/fstui/internal/notify"
	"github.com/nuclear-gumbo/fstui/internal/zfs/health"

	"github.com/nuclear-gumbo/fstui/internal/schedule"

	"github.com/nuclear-gumbo/fstui/internal/check"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/demo"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// The demo build (tag fstui_demo) shows recorded OpenZFS output instead
// of running zfs and zpool: nothing it does reaches a real pool, and it
// needs no ZFS on the machine. It is built as fstui-demo and shipped in
// the demo container image.

// buildLabel is shown before the mode in the status bar.
const buildLabel = "DEMO"

func registerZFS(r *registry.Registry) {
	r.Add(registry.Module{Name: "demo", Kind: registry.Backend, Describe: describeDemo})
}

func describeDemo(context.Context) string {
	v, err := demo.New().Version()
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("recorded output of %s; no ZFS is used", v.Userland)
}

// zfsScreens returns the ZFS views on the demo's recordings.
func zfsScreens(cfg config.Config) (opened, error) {
	cmds := demo.New()
	v, err := cmds.Version()
	if err != nil {
		return opened{}, err
	}
	arc, err := demo.NewARC()
	if err != nil {
		return opened{}, err
	}
	cmds.UseARC(arc)
	sched, err := demo.NewSchedules()
	if err != nil {
		return opened{}, err
	}
	host := schedule.Detect(sched.Root)
	cmds.UseSchedules(sched)
	reader := zfs.NewReader(cmds)
	reader.CronFile = ""                                                                                                      // the host's cron file is not part of the demo
	reader.ReadFile = cmds.ReadFile                                                                                           // nor its module parameters or modprobe.d
	reader.ReadDir = cmds.ReadDir                                                                                             // nor its kstat directories
	o := zfsViews(cfg, zfsSource{reader: reader, version: v, root: true, arc: arc.Read, hosts: hostLister(config.LocalHost)}) // the recordings include pool history
	o.write, o.scheduleHost, o.scheduleRun = demoWrite(cmds, reader, sched), &host, sched
	o.scheduleTasks = func(ctx context.Context) ([]schedule.Task, []schedule.Job, error) {
		return reader.ScheduleTasks(ctx, "/usr/sbin/zpool")
	}
	o.cleanup = func() { _ = sched.Close() } // a temporary directory; nothing to report at exit
	o.notifyTest = func(context.Context, config.Notifications, config.Channel, config.Secrets, string) error {
		return errors.New("the demo sends no notifications")
	}
	return o, nil
}

// checkSource is fstui check on the demo's pools: the same problems and
// kernel events a real check finds, from the recordings.
func checkSource(cfg config.Config) (check.Source, error) {
	cmds := demo.New()
	v, err := cmds.Version()
	if err != nil {
		return nil, err
	}
	r := zfs.NewReader(cmds)
	r.CronFile = ""
	return health.Source{Reader: r, Options: health.Options{Thresholds: cfg.Thresholds, Version: v}}, nil
}

// checkSending prints each message the demo's check would send instead
// of sending it, and keeps its state with the demo's other files, apart
// from a real check's.
func checkSending(string) (func(context.Context, notify.Channel, notify.Message) error, string) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("fstui-demo-%d", os.Geteuid()), "check")
	return func(_ context.Context, ch notify.Channel, m notify.Message) error {
		fmt.Printf("demo, not sent to %s: %s\n", ch.Name, m.Title) // the demo's stand-in for sending
		return nil
	}, dir
}

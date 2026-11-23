// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nozfs && !fstui_demo

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nuclear-gumbo/fstui/internal/notify"

	"github.com/nuclear-gumbo/fstui/internal/schedule"

	"github.com/nuclear-gumbo/fstui/internal/zfs/health"

	"github.com/nuclear-gumbo/fstui/internal/check"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func registerZFS(r *registry.Registry) {
	r.Add(registry.Module{Name: "zfs", Kind: registry.Backend, Describe: describeZFS})
}

// describeZFS reports the OpenZFS userland and kernel module versions, or
// why they could not be read or are not supported.
func describeZFS(ctx context.Context) string {
	tools, err := execute.Resolve(execute.ZFS)
	if err != nil {
		return err.Error()
	}
	v, err := zfs.DetectVersion(ctx, execute.NewRunner(tools))
	if err != nil {
		return err.Error()
	}
	line := fmt.Sprintf("%s (userland), %s (kernel)",
		termtext.New(v.Userland).Display(), termtext.New(v.Kernel).Display())
	if v.Mismatched() {
		line += "; these differ, reboot or reload the zfs module so they match"
	}
	return line
}

// zfsScreens opens the ZFS backend: it resolves and checks the tools,
// refuses OpenZFS older than 2.3, and returns the ZFS views.
func zfsScreens(cfg config.Config) (opened, error) {
	// Optional: without systemctl, scrub timers are not shown; without
	// tee, cp and mv, ARC limits cannot be changed.
	tools, err := execute.ResolveOptional([]execute.Tool{execute.ZFS, execute.Zpool},
		[]execute.Tool{execute.Systemctl, execute.Tee, execute.Cp, execute.Chmod, execute.Mv, execute.Lsblk, execute.RcUpdate, execute.RcService})
	if err != nil {
		return opened{}, fmt.Errorf("not running the ZFS tools: %w\n"+
			"fstui runs zfs and zpool only from /usr/sbin, /usr/bin, /sbin or /bin, and only when "+
			"they and every directory above them are owned by root and writable by no one else", err)
	}
	run := execute.NewRunner(tools)
	run.ReadTimeout = cfg.Limits.ReadTimeout
	v, err := zfs.DetectVersion(context.Background(), run)
	if err != nil {
		return opened{}, err
	}
	reader := newReader(run, cfg)
	o := zfsViews(cfg, zfsSource{reader: reader, version: v, root: os.Geteuid() == 0, arc: zfs.ReadARCStats})
	o.write = zfsWrite(reader, tools)
	o.scheduleTasks = func(ctx context.Context) ([]schedule.Task, []schedule.Job, error) {
		zpool, _ := tools.Path(execute.Zpool)
		return reader.ScheduleTasks(ctx, zpool)
	}
	o.notifyInfo = func() []string { return []string{check.DetectZED("/proc", check.ZEDConfig).Describe()} }
	return o, nil
}

// checkSource opens the ZFS backend for fstui check: its problems, by the
// thresholds in cfg, and its kernel events.
func checkSource(cfg config.Config) (check.Source, error) {
	tools, err := execute.Resolve(execute.ZFS, execute.Zpool)
	if err != nil {
		return nil, fmt.Errorf("not running the ZFS tools: %w", err)
	}
	run := execute.NewRunner(tools)
	run.ReadTimeout = cfg.Limits.ReadTimeout
	v, err := zfs.DetectVersion(context.Background(), run)
	if err != nil {
		return nil, err
	}
	return health.Source{Reader: newReader(run, cfg), Options: health.Options{Thresholds: cfg.Thresholds, Version: v}}, nil
}

// newReader is a ZFS reader with the limits in cfg.
func newReader(run zfs.Commands, cfg config.Config) *zfs.Reader {
	r := zfs.NewReader(run)
	r.HistoryKeep, r.DiffMax, r.HistoryTimeout = cfg.Limits.HistoryEntries, cfg.Limits.DiffEntries, cfg.Limits.HistoryTimeout
	return r
}

// checkSending is how fstui check and watch send, and where they keep
// what they sent: for real, in dir.
func checkSending(dir string) (func(context.Context, notify.Channel, notify.Message) error, string) {
	return notify.Send, dir
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nozfs && !fstui_demo

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/zfsview"
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
		[]execute.Tool{execute.Systemctl, execute.Tee, execute.Cp, execute.Chmod, execute.Mv, execute.Lsblk})
	if err != nil {
		return opened{}, fmt.Errorf("not running the ZFS tools: %w\n"+
			"fstui runs zfs and zpool only from /usr/sbin, /usr/bin, /sbin or /bin, and only when "+
			"they and every directory above them are owned by root and writable by no one else", err)
	}
	run := execute.NewRunner(tools)
	v, err := zfs.DetectVersion(context.Background(), run)
	if err != nil {
		return opened{}, err
	}
	pools := zfsview.NewPools(zfs.NewReader(run), zfsview.PoolsOptions{
		Interval: cfg.Refresh.Pools, DatasetInterval: cfg.Refresh.Datasets, Thresholds: cfg.Thresholds, Superuser: cfg.Superuser,
		Version: v, Root: os.Geteuid() == 0, Now: time.Now,
	})
	reader := zfs.NewReader(run)
	datasets := zfsview.NewDatasets(reader, zfsview.DatasetsOptions{Interval: cfg.Refresh.Datasets, Now: time.Now})
	snapshots := zfsview.NewSnapshots(reader, time.Now)
	volumes := zfsview.NewVolumes(reader, cfg.Refresh.Datasets, time.Now)
	arc := zfsview.NewARC(zfs.ReadARCStats, time.Now)
	io := zfsview.NewIO(reader, time.Now)
	evs := zfsview.NewEvents(reader, zfsview.EventsOptions{
		Root: os.Geteuid() == 0, Retention: cfg.Events.Retention, Filters: cfg.Events.Filters, Now: time.Now,
	})
	dash := zfsview.NewDashboard(pools, evs, io, arc, zfsview.DashboardOptions{
		Panels: cfg.Dashboard.Panels, Columns: cfg.Dashboard.Columns, Now: time.Now,
	})
	// The dashboard comes first in the view bar.
	return opened{
		screens: []ui.Screen{dash, pools, datasets, snapshots, volumes, arc, io, evs},
		write:   zfsWrite(reader, tools),
	}, nil
}

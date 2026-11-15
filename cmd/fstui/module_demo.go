// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/demo"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/zfsview"
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
	reader := zfs.NewReader(cmds)
	reader.CronFile = ""            // the host's cron file is not part of the demo
	reader.ReadFile = cmds.ReadFile // nor its module parameters or modprobe.d
	pools := zfsview.NewPools(reader, zfsview.PoolsOptions{
		Interval: cfg.Refresh.Pools, DatasetInterval: cfg.Refresh.Datasets, Thresholds: cfg.Thresholds, Superuser: cfg.Superuser,
		Version: v, Root: true, Now: time.Now, // the recordings include pool history
	})
	arcView := zfsview.NewARC(arc.Read, time.Now)
	io := zfsview.NewIO(reader, time.Now)
	evs := zfsview.NewEvents(reader, zfsview.EventsOptions{
		Root: true, Retention: cfg.Events.Retention, Filters: cfg.Events.Filters, Now: time.Now,
	})
	dash := zfsview.NewDashboard(pools, evs, io, arcView, zfsview.DashboardOptions{
		Panels: cfg.Dashboard.Panels, Columns: cfg.Dashboard.Columns, Now: time.Now,
	})
	return opened{screens: []ui.Screen{
		dash, pools,
		zfsview.NewDatasets(reader, zfsview.DatasetsOptions{Interval: cfg.Refresh.Datasets, Now: time.Now}),
		zfsview.NewSnapshots(reader, time.Now),
		zfsview.NewVolumes(reader, cfg.Refresh.Datasets, time.Now),
		arcView, io, evs,
	}, write: demoWrite(cmds, reader)}, nil
}

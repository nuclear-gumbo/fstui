// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nozfs || fstui_demo

package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/format"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/zfsview"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// zfsSource is what the ZFS views read: the local machine's tools and
// files, or a remote host's.
type zfsSource struct {
	reader  *zfs.Reader
	version zfs.Version
	root    bool // fstui is root where the pools are
	arc     func() (zfs.ARCStats, error)
	hosts   *ui.HostLister // for the dashboard's hosts panel
}

// zfsViews are the ZFS views over src, the dashboard first in the view
// bar, and the pool names the Alerts tab offers.
func zfsViews(cfg config.Config, src zfsSource) opened {
	settings := &cfg // one copy the views share and update when the Config view saves
	reader := src.reader
	pools := zfsview.NewPools(reader, zfsview.PoolsOptions{
		Interval: cfg.Refresh.Pools, DatasetInterval: cfg.Refresh.Datasets, Thresholds: cfg.Thresholds, Superuser: cfg.Superuser,
		Version: src.version, Root: src.root, Now: time.Now, Settings: settings,
	})
	datasets := zfsview.NewDatasets(reader, zfsview.DatasetsOptions{Interval: cfg.Refresh.Datasets, Now: time.Now, Settings: settings})
	snapshots := zfsview.NewSnapshots(reader, settings, time.Now)
	volumes := zfsview.NewVolumes(reader, settings, time.Now)
	arc := zfsview.NewARC(src.arc, settings, time.Now)
	io := zfsview.NewIO(reader, settings, time.Now)
	evs := zfsview.NewEvents(reader, zfsview.EventsOptions{
		Root: src.root, Retention: cfg.Events.Retention, Filters: cfg.Events.Filters, Now: time.Now, Settings: settings,
	})
	dash := zfsview.NewDashboard(pools, evs, io, arc, zfsview.DashboardOptions{
		Now: time.Now, Settings: settings, Hosts: src.hosts,
	})
	return opened{
		screens:   []ui.Screen{dash, pools, datasets, snapshots, volumes, arc, io, evs},
		poolNames: poolNames(reader, cfg.Limits.ReadTimeout),
	}
}

// poolSummary is "3 pools", with how many are not ONLINE by state.
func poolSummary(ctx context.Context, r *zfs.Reader) string {
	os, err := r.Overviews(ctx)
	if err != nil {
		return "pools not read: " + err.Error()
	}
	states := map[string]int{}
	for _, o := range os {
		if o.Status.State != "ONLINE" {
			states[o.Status.State]++
		}
	}
	parts := []string{fmt.Sprintf("%d %s", len(os), format.Plural(len(os), "pool", "pools"))}
	for _, s := range slices.Sorted(maps.Keys(states)) {
		parts = append(parts, fmt.Sprintf("%d %s", states[s], termtext.New(s).Display()))
	}
	return strings.Join(parts, ", ")
}

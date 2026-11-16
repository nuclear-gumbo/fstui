// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nozfs && !fstui_nowrite && !fstui_demo

package main

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/disks"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/actions"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
	"github.com/nuclear-gumbo/fstui/internal/zfs/plan"
)

// zfsWrite gives the write module the ZFS changes. As root every change
// is offered; otherwise the menu checks the delegated permissions.
func zfsWrite(r *zfs.Reader, tools *execute.Tools) writeHooks {
	b := plan.Builder{R: r, Unprivileged: os.Geteuid() != 0}
	if _, ok := tools.Path(execute.Lsblk); ok {
		src := disks.Source{
			Run: execute.NewRunner(tools), ByIDDir: "/dev/disk/by-id", SysBlock: "/sys/class/block", PoolDevices: r.PoolDevices,
		}
		b.Devices = src.Devices
	}
	if b.Unprivileged {
		b.Permits = permits(r)
	}
	h := writeHooks{tools: tools, offers: actions.ZFSOffers(b, time.Now), rebuild: b.Build}
	if b.Unprivileged {
		h.perms = delegated(r)
	}
	return h
}

// permits reads what zfs allow grants the user on one dataset, for the
// planners' checks of a change's target parent.
func permits(r *zfs.Reader) func(context.Context, string) (map[string]bool, error) {
	return func(ctx context.Context, ds string) (map[string]bool, error) {
		return delegated(r)(ui.Target{Names: []string{ds}})(ctx)
	}
}

// delegated reads, for a selection, what zfs allow grants the user who
// runs fstui, directly, through a group, or to everyone.
func delegated(r *zfs.Reader) func(ui.Target) actions.Permissions {
	return func(t ui.Target) actions.Permissions {
		return func(ctx context.Context) (map[string]bool, error) {
			dsName, _, _ := strings.Cut(t.Names[0], "@")
			ds, err := name.ParseDataset(dsName)
			if err != nil {
				return nil, fmt.Errorf("dataset %q: %w", dsName, err)
			}
			dels, err := r.Allow(ctx, ds)
			if err != nil {
				return nil, err
			}
			u, err := user.Current()
			if err != nil {
				return nil, fmt.Errorf("who is running fstui: %w", err)
			}
			var groups []string
			ids, _ := u.GroupIds() // without groups, only user and everyone grants count
			for _, id := range ids {
				if g, err := user.LookupGroupId(id); err == nil {
					groups = append(groups, g.Name)
				}
			}
			return zfs.Effective(dels, ds.String(), u.Username, groups), nil
		}
	}
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_noremote

package main

import (
	"context"

	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/check"
	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/demo"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

// The demo's other hosts are the nodes of a recorded Proxmox VE cluster
// (demo.Members), each with some of the demo's pools; one cannot be
// reached. Nothing goes over the network.

func registerRemote(r *registry.Registry) {
	r.Add(registry.Module{Name: "remote", Kind: registry.Feature})
}

func addRemoteFlags(fs *pflag.FlagSet) *string {
	return fs.String("host", "", "look at the pools of a node of the demo's cluster")
}

type remoteHost struct {
	opened
	name      string
	privilege string
}

// openHost opens a node of the demo cluster, read-only as a real remote
// host is until 0.9.1.
func openHost(cfg config.Config, choice ui.HostEntry) (remoteHost, error) {
	cmds, err := demo.NewHost(choice.Name)
	if err != nil {
		return remoteHost{}, err
	}
	v, err := cmds.Version()
	if err != nil {
		return remoteHost{}, err
	}
	arc, err := demo.NewARC()
	if err != nil {
		return remoteHost{}, err
	}
	cmds.UseARC(arc)
	reader := demoReader(cmds)
	o := zfsViews(cfg, zfsSource{reader: reader, version: v, root: true, remote: true, arc: arc.Read, hosts: hostLister(choice.Name)})
	return remoteHost{opened: o, name: choice.Name, privilege: remotePrivilege(0)}, nil
}

// demoReader reads the demo's recordings and nothing of this machine.
func demoReader(cmds *demo.Commands) *zfs.Reader {
	r := zfs.NewReader(cmds)
	r.CronFile, r.ReadFile, r.ReadDir = "", cmds.ReadFile, cmds.ReadDir
	return r
}

// remotePrivilege is as for a real host.
func remotePrivilege(uid int) string {
	if uid == 0 {
		return "root over SSH"
	}
	return "unprivileged over SSH"
}

// hostLister lists the demo machine and the cluster's nodes.
func hostLister(current string) *ui.HostLister {
	if current == "" {
		current = config.LocalHost
	}
	return &ui.HostLister{
		Entries: func(config.Config) []ui.HostEntry {
			out := []ui.HostEntry{{Name: config.LocalHost, Current: current == config.LocalHost}}
			m, err := demo.Members()
			if err != nil {
				return out
			}
			for _, n := range m.Nodes {
				out = append(out, ui.HostEntry{Name: n.Name, Destination: "root@" + n.IP.String(), Cluster: m.Cluster, Current: current == n.Name})
			}
			return out
		},
		Status: func(ctx context.Context, _ config.Config, e ui.HostEntry) (string, []ui.HostEntry) {
			cmds := demo.New()
			if e.Name != config.LocalHost {
				var err error
				if cmds, err = demo.NewHost(e.Name); err != nil {
					return err.Error(), nil
				}
			}
			return poolSummary(ctx, demoReader(cmds)), nil
		},
	}
}

// remoteRuns: the demo's fstui check looks at its own pools only.
func remoteRuns(_ check.Run, hosts []config.Host) ([]check.Run, func(), []string) {
	if len(hosts) == 0 {
		return nil, func() {}, nil
	}
	return nil, func() {}, []string{"the demo checks its own pools only; notifications.hosts is not used"}
}

// hostsNotice is nothing: H lists the demo's cluster.
func hostsNotice(config.Config) string { return "" }

// configView adds the Hosts tab; the demo's hosts are its cluster, and
// a host added there is listed and tested like the cluster's nodes.
func configView(c *ui.Config, user string, cfg config.Config) *ui.Config {
	return c.WithHosts(ui.NewHosts(user, cfg, hostLister(config.LocalHost)))
}

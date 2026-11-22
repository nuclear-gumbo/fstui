// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_noremote && !fstui_nozfs && !fstui_demo

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/statedir"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func registerRemote(r *registry.Registry) {
	r.Add(registry.Module{Name: "remote", Kind: registry.Feature})
}

// addRemoteFlags adds --host.
func addRemoteFlags(fs *pflag.FlagSet) *string {
	return fs.String("host", "", "look at the pools of a host listed under hosts: in config.yaml, over SSH")
}

// remoteHost is a host fstui opened: its views and what the status bar
// says about it.
type remoteHost struct {
	opened
	name      string
	privilege string
}

// openHost connects to the host named name, checks it (the probe) and
// returns its ZFS views. Changes on remote hosts come in 0.9.1: nothing
// is offered for writing, and the Schedules tab keeps to this machine.
func openHost(cfg config.Config, name string) (remoteHost, error) {
	h, ok := cfg.HostNamed(name)
	if !ok {
		return remoteHost{}, unknownHost(cfg, name)
	}
	tools, err := execute.Resolve(execute.SSH)
	if err != nil {
		return remoteHost{}, fmt.Errorf("not running ssh: %w", err)
	}
	dir, err := sshDir()
	if err != nil {
		return remoteHost{}, err
	}
	run := execute.NewRunner(tools)
	run.ReadTimeout = cfg.Limits.ReadTimeout
	rem := execute.NewRemote(run, execute.Host{
		Destination: h.Destination, ControlDir: dir,
		ConnectTimeout: cfg.Remote.ConnectTimeout, AliveInterval: cfg.Remote.AliveInterval, Persist: cfg.Remote.Persist,
	})
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Remote.ConnectTimeout+cfg.Limits.ReadTimeout)
	defer cancel()
	p, err := rem.Probe(ctx)
	if err == nil {
		err = p.Require(execute.RemoteEnv, execute.ZFS, execute.Zpool)
	}
	if err != nil {
		rem.Close()
		return remoteHost{}, fmt.Errorf("host %s (%s): %w", h.Name, h.Destination, err)
	}
	v, err := zfs.DetectVersionOn(ctx, rem)
	if err != nil {
		rem.Close()
		return remoteHost{}, fmt.Errorf("host %s: %w", h.Name, err)
	}
	reader := newReader(rem, cfg)
	reader.ReadFile, reader.ReadDir = rem.ReadFile, rem.ReadDir
	o := zfsViews(cfg, zfsSource{reader: reader, version: v, root: p.UID == 0, arc: reader.ARCStats})
	o.cleanup = rem.Close
	return remoteHost{opened: o, name: h.Name, privilege: remotePrivilege(p.UID)}, nil
}

// sshDir is the private directory of ssh's connection sockets.
func sshDir() (string, error) {
	run, err := statedir.RuntimeDir()
	if err != nil {
		return "", fmt.Errorf("no directory for ssh's connections: %w", err)
	}
	dir := filepath.Join(run, "ssh")
	for _, d := range []string{run, dir} {
		if err := statedir.Private(d); err != nil {
			return "", fmt.Errorf("ssh's connections: %w", err)
		}
	}
	return dir, nil
}

// remotePrivilege is the status bar's privilege field for a host; the
// host field beside it names the host.
func remotePrivilege(uid int) string {
	if uid == 0 {
		return "root over SSH"
	}
	return "uid " + strconv.Itoa(uid) + " over SSH"
}

func unknownHost(cfg config.Config, name string) error {
	if len(cfg.Hosts) == 0 {
		return fmt.Errorf("no host %q: config.yaml lists no hosts; add them under hosts: with a name and a destination", termtext.New(name).Display())
	}
	names := make([]string, len(cfg.Hosts))
	for i, h := range cfg.Hosts {
		names[i] = h.Name
	}
	return fmt.Errorf("no host %q in config.yaml; the hosts are %s", termtext.New(name).Display(), strings.Join(names, ", "))
}

// hostsNotice is nothing: this build reads hosts.
func hostsNotice(config.Config) string { return "" }

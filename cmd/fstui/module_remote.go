// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_noremote && !fstui_nozfs && !fstui_demo

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/check"
	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/events"
	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/problem"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/statedir"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/health"
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

// remoteRuns are the checks of hosts, each like local but reading the
// host over ssh and keeping its own state. A host that cannot be
// reached or fails the probe still gets a run: its failure is then sent
// once, as that host's problem, and cleared when it is back.
func remoteRuns(local check.Run, hosts []config.Host) ([]check.Run, func(), []string) {
	if len(hosts) == 0 {
		return nil, func() {}, nil
	}
	cfg := local.Config
	tools, err := execute.Resolve(execute.SSH)
	if err != nil {
		return nil, func() {}, []string{"not checking other hosts: ssh: " + err.Error()}
	}
	dir, err := sshDir()
	if err != nil {
		return nil, func() {}, []string{"not checking other hosts: " + err.Error()}
	}
	var runs []check.Run
	var remotes []*execute.Remote
	for _, h := range hosts {
		run := execute.NewRunner(tools)
		run.ReadTimeout = cfg.Limits.ReadTimeout
		rem := execute.NewRemote(run, execute.Host{
			Destination: h.Destination, ControlDir: dir,
			ConnectTimeout: cfg.Remote.ConnectTimeout, AliveInterval: cfg.Remote.AliveInterval, Persist: cfg.Remote.Persist,
		})
		remotes = append(remotes, rem)
		r := local
		r.Host = h.Name
		r.StateDir = filepath.Join(local.StateDir, "hosts", h.Name)
		r.Source, r.Boot, r.ZED = hostSource(cfg, rem)
		runs = append(runs, r)
	}
	return runs, func() {
		for _, rem := range remotes {
			rem.Close()
		}
	}, nil
}

// hostSource probes the host and reads what a check needs besides its
// pools: the boot ID, so kernel events start over after a reboot, and
// whether ZED sends them there.
func hostSource(cfg config.Config, rem *execute.Remote) (check.Source, string, check.ZED) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Remote.ConnectTimeout+cfg.Limits.ReadTimeout)
	defer cancel()
	p, err := rem.Probe(ctx)
	if err == nil {
		err = p.Require(execute.RemoteEnv, execute.ZFS, execute.Zpool)
	}
	var v zfs.Version
	if err == nil {
		v, err = zfs.DetectVersionOn(ctx, rem)
	}
	if err != nil {
		return failedSource{err}, "", check.ZED{}
	}
	reader := newReader(rem, cfg)
	reader.ReadFile, reader.ReadDir = rem.ReadFile, rem.ReadDir
	boot, _ := rem.ReadFile(bootIDPath) // none: kernel events start over, as on a first run
	_, zedErr := rem.Read(ctx, execute.Argv{Tool: execute.Systemctl, Options: []string{"is-active", "--quiet", "zfs-zed.service"}})
	rc, _ := rem.ReadFile(check.ZEDConfig) // none: ZED sends nothing
	return health.Source{Reader: reader, Options: health.Options{Thresholds: cfg.Thresholds, Version: v}},
		strings.TrimSpace(string(boot)), check.ZEDFrom(zedErr == nil, rc)
}

// failedSource is a host that could not be read: every check reports
// why.
type failedSource struct{ err error }

func (f failedSource) Measures(context.Context, time.Time, map[string]bool) ([]problem.Measure, error) {
	return nil, f.err
}

func (f failedSource) Events(context.Context) ([]events.Event, int, error) { return nil, 0, f.err }

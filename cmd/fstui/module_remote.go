// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_noremote && !fstui_nozfs && !fstui_demo

package main

import (
	"context"
	"fmt"
	"os"
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
	"github.com/nuclear-gumbo/fstui/internal/pve"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/statedir"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/ui"
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

// openHost connects to the chosen host, checks it (the probe) and
// returns its ZFS views. Changes on remote hosts come in 0.9.1: nothing
// is offered for writing, and the Schedules tab keeps to this machine.
func openHost(cfg config.Config, choice ui.HostEntry) (remoteHost, error) {
	h, err := targetOf(cfg, choice)
	if err != nil {
		return remoteHost{}, err
	}
	rem, err := connect(cfg, h.Host)
	if err != nil {
		return remoteHost{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Remote.ConnectTimeout+cfg.Limits.ReadTimeout)
	defer cancel()
	p, v, err := checkHost(ctx, rem)
	if err != nil {
		rem.Close()
		return remoteHost{}, fmt.Errorf("host %s (%s): %w", h.name, h.Destination, err)
	}
	reader := newReader(rem, cfg)
	reader.ReadFile, reader.ReadDir = rem.ReadFile, rem.ReadDir
	o := zfsViews(cfg, zfsSource{reader: reader, version: v, root: p.UID == 0, remote: true, arc: reader.ARCStats, hosts: hostLister(h.name)})
	o.cleanup = rem.Close
	return remoteHost{opened: o, name: h.name, privilege: remotePrivilege(p.UID)}, nil
}

// checkHost probes the host and reads its OpenZFS version: what must
// hold before anything else is read there.
func checkHost(ctx context.Context, rem *execute.Remote) (execute.Probe, zfs.Version, error) {
	p, err := rem.Probe(ctx)
	if err != nil {
		return p, zfs.Version{}, err
	}
	if err := p.Require(execute.RemoteEnv, execute.ZFS, execute.Zpool); err != nil {
		return p, zfs.Version{}, err
	}
	v, err := zfs.DetectVersionOn(ctx, rem)
	return p, v, err
}

// target is a host to connect to, with the name fstui shows for it.
type target struct {
	execute.Host
	name string
}

// targetOf is the chosen host: one from hosts:, or a Proxmox VE node the
// host list found. A node of this machine's own cluster is reached as
// the cluster reaches it (PVE::SSHInfo).
func targetOf(cfg config.Config, choice ui.HostEntry) (target, error) {
	if h, ok := cfg.HostNamed(choice.Name); ok {
		return target{Host: execute.Host{Destination: h.Destination}, name: h.Name}, nil
	}
	if choice.Destination == "" {
		return target{}, unknownHost(cfg, choice.Name)
	}
	t := target{Host: execute.Host{Destination: choice.Destination}, name: choice.Name}
	if m, ok := localMembers(); ok && choice.Cluster == m.Cluster {
		t.HostKeyAlias = choice.Name
		if f := filepath.Join("/etc/pve/nodes", choice.Name, "ssh_known_hosts"); fileExists(f) {
			t.KnownHostsFile = f
		}
	}
	return t, nil
}

// connect is a connection to h with fstui's settings; nothing is run
// until Probe.
func connect(cfg config.Config, h execute.Host) (*execute.Remote, error) {
	tools, err := execute.Resolve(execute.SSH)
	if err != nil {
		return nil, fmt.Errorf("not running ssh: %w", err)
	}
	dir, err := sshDir()
	if err != nil {
		return nil, err
	}
	run := execute.NewRunner(tools)
	run.ReadTimeout = cfg.Limits.ReadTimeout
	h.ControlDir = dir
	h.ConnectTimeout, h.AliveInterval, h.Persist = cfg.Remote.ConnectTimeout, cfg.Remote.AliveInterval, cfg.Remote.Persist
	return execute.NewRemote(run, h), nil
}

// localMembers is this machine's Proxmox VE cluster, when fstui runs as
// root on a node of one.
func localMembers() (pve.Members, bool) {
	b, err := os.ReadFile(pve.MembersPath)
	if err != nil {
		return pve.Members{}, false
	}
	m, err := pve.ParseMembers(b)
	return m, err == nil && m.Cluster != ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hostLister is what H lists: this machine, the hosts in config.yaml,
// the other nodes of this machine's Proxmox VE cluster, and those of
// each listed host's cluster as each is read.
func hostLister(current string) *ui.HostLister {
	if current == "" {
		current = config.LocalHost
	}
	return &ui.HostLister{
		Entries: func(cfg config.Config) []ui.HostEntry {
			out := []ui.HostEntry{{Name: config.LocalHost, Current: current == config.LocalHost}}
			for _, h := range cfg.Hosts {
				out = append(out, ui.HostEntry{Name: h.Name, Destination: h.Destination, Current: current == h.Name})
			}
			if m, ok := localMembers(); ok {
				out = append(out, nodes(m, cfg, "root@", current, true)...)
			}
			return out
		},
		Status: func(ctx context.Context, cfg config.Config, e ui.HostEntry) (string, []ui.HostEntry) {
			ctx, cancel := context.WithTimeout(ctx, cfg.Remote.ConnectTimeout+cfg.Limits.ReadTimeout)
			defer cancel()
			return hostStatus(ctx, cfg, e, current)
		},
	}
}

// nodes are the cluster's nodes other than this one and the hosts
// already listed, reached as user@node, or by address when byIP.
func nodes(m pve.Members, cfg config.Config, user, current string, byIP bool) []ui.HostEntry {
	var out []ui.HostEntry
	for _, n := range m.Nodes {
		if _, listed := cfg.HostNamed(n.Name); listed || n.Name == m.Self || n.Name == config.LocalHost {
			continue
		}
		dest := user + n.Name
		if byIP && n.IP.IsValid() {
			dest = user + n.IP.String()
		}
		if execute.CheckDestination(dest) != nil {
			continue
		}
		out = append(out, ui.HostEntry{Name: n.Name, Destination: dest, Cluster: m.Cluster, Current: current == n.Name})
	}
	return out
}

// hostStatus reads a host for the list: its pools in a few words, and the
// other nodes of its Proxmox VE cluster.
func hostStatus(ctx context.Context, cfg config.Config, e ui.HostEntry, current string) (string, []ui.HostEntry) {
	if e.Name == config.LocalHost {
		tools, err := execute.Resolve(execute.ZFS, execute.Zpool)
		if err != nil {
			return "not read: " + err.Error(), nil
		}
		return poolSummary(ctx, zfs.NewReader(execute.NewRunner(tools))), nil
	}
	t, err := targetOf(cfg, e)
	if err != nil {
		return err.Error(), nil
	}
	rem, err := connect(cfg, t.Host)
	if err != nil {
		return err.Error(), nil
	}
	defer rem.Close()
	if _, _, err := checkHost(ctx, rem); err != nil {
		return err.Error(), nil
	}
	summary := poolSummary(ctx, zfs.NewReader(rem))
	b, err := rem.ReadFile(pve.MembersPath)
	if err != nil {
		return summary, nil
	}
	m, err := pve.ParseMembers(b)
	if err != nil || m.Cluster == "" {
		return summary, nil
	}
	user := ""
	if u, _, ok := strings.Cut(t.Destination, "@"); ok {
		user = u + "@"
	}
	return summary, nodes(m, cfg, user, current, false)
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
	var runs []check.Run
	var remotes []*execute.Remote
	for _, h := range hosts {
		rem, err := connect(cfg, execute.Host{Destination: h.Destination})
		if err != nil {
			return nil, func() {}, []string{"not checking other hosts: " + err.Error()}
		}
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
	_, v, err := checkHost(ctx, rem)
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

// configView adds the Hosts tab.
func configView(c *ui.Config, user string, cfg config.Config) *ui.Config {
	return c.WithHosts(ui.NewHosts(user, cfg, hostLister(config.LocalHost)))
}

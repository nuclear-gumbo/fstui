// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_noremote || fstui_nozfs || fstui_demo

package main

import (
	"errors"

	"github.com/nuclear-gumbo/fstui/internal/check"
	"github.com/spf13/pflag"

	"github.com/nuclear-gumbo/fstui/internal/config"
	"github.com/nuclear-gumbo/fstui/internal/registry"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// registerRemote is empty in builds that leave out remote hosts (and,
// for now, in the demo, which gets its own hosts in a later change).
func registerRemote(*registry.Registry) {}

// addRemoteFlags adds no --host.
func addRemoteFlags(*pflag.FlagSet) *string { return new(string) }

type remoteHost struct {
	opened
	name      string
	privilege string
}

// openHost is never reached: there is no --host to ask for it.
func openHost(config.Config, ui.HostEntry) (remoteHost, error) {
	return remoteHost{}, errors.New("this build of fstui leaves out remote hosts")
}

// hostsNotice says that hosts: is read by nothing in this build.
func hostsNotice(cfg config.Config) string {
	if len(cfg.Hosts) == 0 {
		return ""
	}
	return "hosts: in config.yaml is ignored: this build of fstui leaves out the remote module"
}

// remoteRuns checks no other host in this build.
func remoteRuns(_ check.Run, hosts []config.Host) ([]check.Run, func(), []string) {
	if len(hosts) == 0 {
		return nil, func() {}, nil
	}
	return nil, func() {}, []string{"notifications.hosts names other hosts, which this build of fstui does not check: it leaves out the remote module"}
}

// hostLister lists no hosts: H does nothing in this build.
func hostLister(config.Config, string) *ui.HostLister { return nil }

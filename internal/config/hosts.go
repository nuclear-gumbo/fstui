// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Host is another machine fstui reads over SSH: a name to show and to
// give --host, and the destination ssh connects to.
type Host struct {
	Name        string
	Destination string
}

// Remote is how fstui connects to hosts (docs/design/remote.md).
type Remote struct {
	ConnectTimeout time.Duration // connecting and the key exchange
	AliveInterval  time.Duration // how often an idle connection is checked
	Persist        time.Duration // how long a shared connection stays open unused
}

func defaultRemote() Remote {
	return Remote{ConnectTimeout: 10 * time.Second, AliveInterval: 15 * time.Second, Persist: time.Minute}
}

// hostsMax bounds hosts: a cluster of this size is already more than a
// list on one screen shows.
const hostsMax = 64

// LocalHost is the name of the machine fstui runs on; no host takes it.
const LocalHost = "local"

// hostName keeps host names usable after --host and in messages.
var hostLabel = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// setHosts reads the list of hosts. A host with a problem is reported
// and left out; the others apply. A file that sets hosts replaces the
// hosts of the files before it.
func setHosts(a *applier, c *Config, n *yaml.Node) error {
	out, err := namedList(a, n, "hosts", "host", hostsMax, hostOf, func(h Host) string { return h.Name })
	c.Hosts = out
	return err
}

func hostOf(n *yaml.Node) (Host, error) {
	var h Host
	if n.Kind != yaml.MappingNode {
		return h, errors.New("want a mapping with name and destination")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		s, err := scalar(v, "!!str", "a string")
		if err != nil {
			return h, fmt.Errorf("%s: %w", k.Value, err)
		}
		switch k.Value {
		case "name":
			h.Name = s
		case "destination":
			h.Destination = s
		default:
			return h, fmt.Errorf("%s: not a host setting; a host has name and destination", k.Value)
		}
	}
	switch {
	case !hostLabel.MatchString(h.Name):
		return h, errors.New("name: want 1 to 40 lower-case letters, digits, - and _")
	case h.Name == LocalHost:
		return h, fmt.Errorf("name: %q is the machine fstui runs on", LocalHost)
	}
	if err := execute.CheckDestination(h.Destination); err != nil {
		return h, fmt.Errorf("destination: %w; give an alias from ~/.ssh/config or user@host", err)
	}
	return h, nil
}

// HostNamed returns the host with this name.
func (c Config) HostNamed(name string) (Host, bool) {
	for _, h := range c.Hosts {
		if h.Name == name {
			return h, true
		}
	}
	return Host{}, false
}

// setNotifyHosts reads notifications.hosts: a name or a list of names.
// Whether each names a host is checked when the hosts are known (every
// file read), by CheckedHosts.
func setNotifyHosts(_ *applier, c *Config, n *yaml.Node) error {
	names, err := stringList(n)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !hostLabel.MatchString(name) {
			return fmt.Errorf("%q: want host names from hosts:, or %s for this machine", name, LocalHost)
		}
	}
	c.Notifications.Hosts = names
	return nil
}

// CheckedHosts are the hosts fstui check looks at, local first, and a
// problem for each name that is neither local nor a listed host.
func (c Config) CheckedHosts() (local bool, hosts []Host, problems []string) {
	if len(c.Notifications.Hosts) == 0 {
		return true, nil, nil
	}
	for _, name := range c.Notifications.Hosts {
		if name == LocalHost {
			local = true
			continue
		}
		if h, ok := c.HostNamed(name); ok {
			hosts = append(hosts, h)
		} else {
			problems = append(problems, fmt.Sprintf("notifications.hosts: no host named %q under hosts:", name))
		}
	}
	return local, hosts, problems
}

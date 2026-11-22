// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestHosts(t *testing.T) {
	c, problems := Parse("c.yaml", []byte(`hosts:
  - {name: pve1, destination: root@10.10.1.101}
  - {name: nas, destination: nas}
  - {name: pve1, destination: other}
  - {name: local, destination: x}
  - {name: bad, destination: "x;id"}
  - {name: odd, destination: x, user: root}
`))
	if len(c.Hosts) != 2 || c.Hosts[0] != (Host{"pve1", "root@10.10.1.101"}) || c.Hosts[1].Name != "nas" {
		t.Errorf("hosts = %+v", c.Hosts)
	}
	want := []string{"already listed", `"local" is the machine`, "destination:", "not a host setting"}
	if len(problems) != len(want) {
		t.Fatalf("problems = %v", problems)
	}
	for i, w := range want {
		if !strings.Contains(problems[i].Msg, w) {
			t.Errorf("problem %d = %v, want %q", i, problems[i], w)
		}
	}
	if h, ok := c.HostNamed("nas"); !ok || h.Destination != "nas" {
		t.Errorf("HostNamed(nas) = %v %v", h, ok)
	}
	if _, ok := c.HostNamed("pve9"); ok {
		t.Error("HostNamed found a host that is not listed")
	}
}

// A later file's hosts replace the earlier list, as for channels.
func TestHostsDefaults(t *testing.T) {
	d := Default()
	if len(d.Hosts) != 0 || d.Remote.ConnectTimeout == 0 || d.Remote.Persist == 0 {
		t.Errorf("defaults: %+v %+v", d.Hosts, d.Remote)
	}
}

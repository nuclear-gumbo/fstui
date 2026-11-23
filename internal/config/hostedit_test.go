// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

// The edit keeps the rest of the file and reads back as written.
func TestEditHosts(t *testing.T) {
	cur := []byte("# my settings\ntheme: nord\nhosts:\n  - {name: old, destination: old}\nnotifications:\n  repeat: 12h\n")
	next, err := EditHosts(cur, []Host{{"pve1", "root@pve1"}, {"nas", "nas"}}, []string{LocalHost, "pve1"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(next)
	for _, want := range []string{"# my settings", "theme: nord", "repeat: 12h"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %q:\n%s", want, s)
		}
	}
	c, problems := Parse("c.yaml", next)
	if len(problems) > 0 {
		t.Fatalf("%v\n%s", problems, s)
	}
	if len(c.Hosts) != 2 || c.Hosts[0] != (Host{"pve1", "root@pve1"}) || strings.Join(c.Notifications.Hosts, ",") != "local,pve1" {
		t.Errorf("read back %+v %v\n%s", c.Hosts, c.Notifications.Hosts, s)
	}
	// nil leaves notifications.hosts as it was; no hosts writes an empty list.
	again, err := EditHosts(next, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = Parse("c.yaml", again)
	if len(c.Hosts) != 0 || strings.Join(c.Notifications.Hosts, ",") != "local,pve1" {
		t.Errorf("after removing every host: %+v %v", c.Hosts, c.Notifications.Hosts)
	}
}

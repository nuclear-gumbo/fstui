// SPDX-License-Identifier: GPL-3.0-or-later

// Package pve reads how a Proxmox VE cluster describes itself: which
// nodes it has (/etc/pve/.members) and which ZFS storage each node holds
// (/etc/pve/storage.cfg). The formats are those pmxcfs writes and
// pve-common's SectionConfig reads; both files come from the cluster and
// are untrusted.
package pve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// MembersPath and StoragePath are where a node keeps the files.
const (
	MembersPath = "/etc/pve/.members"
	StoragePath = "/etc/pve/storage.cfg"
)

// Node is one member of a cluster.
type Node struct {
	Name   string
	ID     int
	Online bool
	IP     netip.Addr // invalid when pmxcfs gave none
}

// Members is a node's view of its cluster. Cluster is empty on a node
// that is not in one; Nodes then holds only Self.
type Members struct {
	Self    string
	Cluster string
	Quorate bool
	Nodes   []Node // by name
}

// nodeName is what Proxmox VE accepts as a node name: a host name.
var nodeName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ErrMembers is returned for a .members file fstui cannot use.
var ErrMembers = errors.New("not a Proxmox VE membership file")

// ParseMembers reads .members, the JSON pmxcfs writes
// (cfs_create_memberlist_msg in src/pmxcfs/status.c).
func ParseMembers(b []byte) (Members, error) {
	var raw struct {
		NodeName string `json:"nodename"`
		Cluster  *struct {
			Name    string `json:"name"`
			Quorate int    `json:"quorate"`
		} `json:"cluster"`
		NodeList map[string]struct {
			ID     int    `json:"id"`
			Online int    `json:"online"`
			IP     string `json:"ip"`
		} `json:"nodelist"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Members{}, fmt.Errorf("%w: %w", ErrMembers, err)
	}
	if !nodeName.MatchString(raw.NodeName) {
		return Members{}, fmt.Errorf("%w: node name %q", ErrMembers, raw.NodeName)
	}
	m := Members{Self: raw.NodeName}
	if raw.Cluster == nil {
		m.Nodes = []Node{{Name: raw.NodeName, Online: true}}
		return m, nil
	}
	m.Cluster, m.Quorate = raw.Cluster.Name, raw.Cluster.Quorate == 1
	for n, v := range raw.NodeList {
		if !nodeName.MatchString(n) {
			return Members{}, fmt.Errorf("%w: node name %q", ErrMembers, n)
		}
		node := Node{Name: n, ID: v.ID, Online: v.Online == 1}
		if v.IP != "" {
			ip, err := netip.ParseAddr(v.IP)
			if err != nil {
				return Members{}, fmt.Errorf("%w: address of %s: %w", ErrMembers, n, err)
			}
			node.IP = ip
		}
		m.Nodes = append(m.Nodes, node)
	}
	// pmxcfs lists the node itself whenever it writes a cluster.
	if !slices.ContainsFunc(m.Nodes, func(n Node) bool { return n.Name == m.Self }) {
		return Members{}, fmt.Errorf("%w: %s is not in its own cluster's node list", ErrMembers, m.Self)
	}
	slices.SortFunc(m.Nodes, func(a, b Node) int { return strings.Compare(a.Name, b.Name) })
	return m, nil
}

// Storage is a ZFS storage of the cluster.
type Storage struct {
	ID string
	// Type is "zfspool", a pool on the nodes, or "zfs", ZFS over iSCSI:
	// a pool on a storage box (Portal), not on any node.
	Type    string
	Pool    name.Pool    // the pool the storage is in
	Dataset name.Dataset // the dataset it uses: the pool, or one in it
	Nodes   []string     // the nodes it is on; nil for every node
	Portal  string       // ZFS over iSCSI only
	Disable bool
}

// ParseStorage reads storage.cfg in pve-common's SectionConfig format
// (PVE::SectionConfig::parse_config) and returns its ZFS storages; other
// types are skipped. A ZFS section fstui cannot read is an error.
func ParseStorage(b []byte) ([]Storage, error) {
	var r sections
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if err := r.line(sc.Text()); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("storage.cfg: %w", err)
	}
	if err := r.finish(); err != nil {
		return nil, err
	}
	return r.out, nil
}

// sections reads storage.cfg a line at a time; props is the open ZFS
// section's properties, nil outside one.
type sections struct {
	out     []Storage
	props   map[string]string
	id, typ string
}

func (r *sections) line(line string) error {
	switch {
	case strings.HasPrefix(strings.TrimLeft(line, " \t"), "#"):
		return nil
	case strings.TrimSpace(line) == "": // a blank line ends a section
		return r.finish()
	case line[0] != ' ' && line[0] != '\t':
		if err := r.finish(); err != nil {
			return err
		}
		m := sectionHeader.FindStringSubmatch(line)
		if m == nil {
			return fmt.Errorf("storage.cfg: not a section header: %q", line)
		}
		r.typ, r.id = strings.ToLower(m[1]), m[2]
		if r.typ == "zfspool" || r.typ == "zfs" {
			r.props = map[string]string{}
		}
	case r.props != nil:
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		r.props[k] = strings.TrimSpace(v)
	}
	return nil
}

// finish ends the open ZFS section, if any.
func (r *sections) finish() error {
	if r.props == nil {
		return nil
	}
	s, err := storageOf(r.id, r.typ, r.props)
	r.props = nil
	if err != nil {
		return err
	}
	r.out = append(r.out, s)
	return nil
}

// sectionHeader is parse_section_header's "type: id".
var sectionHeader = regexp.MustCompile(`^(\S+):\s*(\S+)\s*$`)

func storageOf(id, typ string, props map[string]string) (Storage, error) {
	ds, err := name.ParseDataset(props["pool"])
	if err != nil {
		return Storage{}, fmt.Errorf("storage.cfg, %s: pool: %w", id, err)
	}
	s := Storage{ID: id, Type: typ, Pool: ds.Pool(), Dataset: ds, Portal: props["portal"], Disable: props["disable"] == "1"}
	if n := props["nodes"]; n != "" {
		for _, node := range strings.Split(n, ",") {
			if node = strings.TrimSpace(node); !nodeName.MatchString(node) {
				return Storage{}, fmt.Errorf("storage.cfg, %s: node name %q", id, node)
			}
			s.Nodes = append(s.Nodes, strings.TrimSpace(node))
		}
	}
	return s, nil
}

// On reports whether the storage is on node.
func (s Storage) On(node string) bool {
	return s.Type == "zfspool" && (s.Nodes == nil || slices.Contains(s.Nodes, node))
}

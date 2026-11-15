// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// GrantScope is where a delegated permission applies, relative to the
// dataset it was set on (zfs-allow(8): -l, -d, or both by default).
type GrantScope int

// Scopes.
const (
	Local GrantScope = iota + 1
	Descendent
	LocalAndDescendent
)

// Grant is one delegation: who may do what.
type Grant struct {
	Scope GrantScope
	Who   string        // "user", "group" or "everyone"
	Name  termtext.Text // user or group name; empty for everyone
	Perms []string      // permissions and @sets
}

// PermissionSet is a named set of permissions defined on a dataset.
type PermissionSet struct {
	Name  string // with its @
	Perms []string
}

// Delegation is what zfs allow lists for one dataset: the one asked
// about comes first, then each ancestor that has delegations.
type Delegation struct {
	Dataset    termtext.Text
	Sets       []PermissionSet
	CreateTime []string // granted to whoever creates a dataset below
	Grants     []Grant
}

// Allow reads the delegated permissions of ds and its ancestors with zfs
// allow, which has no JSON output in 2.3.9 and needs no root.
func (r *Reader) Allow(ctx context.Context, ds name.Dataset) ([]Delegation, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.ZFS, Options: []string{"allow"}, Operands: []string{ds.String()}})
	if err != nil {
		return nil, err
	}
	return ParseAllow(out)
}

// allowSections are zfs allow's section headings.
var allowSections = map[string]GrantScope{
	"Local permissions:":            Local,
	"Descendent permissions:":       Descendent,
	"Local+Descendent permissions:": LocalAndDescendent,
}

const (
	setsHeading       = "Permission sets:"
	createTimeHeading = "Create time permissions:"
	blockPrefix       = "---- Permissions on "
)

// ParseAllow parses zfs allow output: a "---- Permissions on <dataset>"
// line per dataset, section headings, and one tab-indented entry per
// line. Nothing delegated anywhere gives no output at all.
func ParseAllow(data []byte) ([]Delegation, error) {
	var out []Delegation
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, blockPrefix):
			ds := strings.TrimRight(strings.TrimPrefix(line, blockPrefix), "- ")
			out = append(out, Delegation{Dataset: termtext.New(ds)})
			section = ""
		case !strings.HasPrefix(line, "\t"):
			section = line
		case len(out) == 0:
			return nil, fmt.Errorf("zfs allow: entry before a dataset: %q", termtext.New(line).Display())
		default:
			if err := addEntry(&out[len(out)-1], section, strings.TrimPrefix(line, "\t")); err != nil {
				return nil, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("zfs allow: %w", err)
	}
	return out, nil
}

func addEntry(d *Delegation, section, entry string) error {
	fields := strings.Fields(entry)
	perms := func(s string) []string { return strings.Split(s, ",") }
	switch {
	case section == setsHeading && len(fields) == 2:
		d.Sets = append(d.Sets, PermissionSet{Name: fields[0], Perms: perms(fields[1])})
	case section == createTimeHeading && len(fields) == 1:
		d.CreateTime = perms(fields[0])
	case allowSections[section] != 0 && len(fields) == 2 && fields[0] == "everyone":
		d.Grants = append(d.Grants, Grant{Scope: allowSections[section], Who: "everyone", Perms: perms(fields[1])})
	case allowSections[section] != 0 && len(fields) == 3 && (fields[0] == "user" || fields[0] == "group"):
		d.Grants = append(d.Grants, Grant{Scope: allowSections[section], Who: fields[0], Name: termtext.New(fields[1]), Perms: perms(fields[2])})
	default:
		return fmt.Errorf("zfs allow: unexpected entry %q under %q", termtext.New(entry).Display(), termtext.New(section).Display())
	}
	return nil
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import "strings"

// Effective returns the permissions delegated to a user on ds: grants to
// the user, to one of their groups, or to everyone, set on ds itself
// (local scope) or on an ancestor (descendent scope), with permission
// sets expanded. dels is what Allow returned for ds.
func Effective(dels []Delegation, ds, user string, groups []string) map[string]bool {
	sets := map[string][]string{}
	for _, d := range dels {
		for _, s := range d.Sets {
			if _, seen := sets[s.Name]; !seen { // the nearest definition wins
				sets[s.Name] = s.Perms
			}
		}
	}
	out := map[string]bool{}
	for _, d := range dels {
		here := d.Dataset.Raw() == ds
		for _, g := range d.Grants {
			if !scopeApplies(g.Scope, here) || !grantee(g, user, groups) {
				continue
			}
			for _, p := range g.Perms {
				expand(p, sets, out, 0)
			}
		}
	}
	return out
}

func scopeApplies(s GrantScope, here bool) bool {
	switch s {
	case Local:
		return here
	case Descendent:
		return !here
	case LocalAndDescendent:
		return true
	}
	return false
}

func grantee(g Grant, user string, groups []string) bool {
	switch g.Who {
	case "everyone":
		return true
	case "user":
		return g.Name.Raw() == user
	case "group":
		for _, gr := range groups {
			if g.Name.Raw() == gr {
				return true
			}
		}
	}
	return false
}

// maxSetDepth bounds nested sets; ZFS refuses cycles, this guards anyway.
const maxSetDepth = 8

func expand(p string, sets map[string][]string, out map[string]bool, depth int) {
	if !strings.HasPrefix(p, "@") {
		out[p] = true
		return
	}
	if depth >= maxSetDepth {
		return
	}
	for _, q := range sets[p] {
		expand(q, sets, out, depth+1)
	}
}

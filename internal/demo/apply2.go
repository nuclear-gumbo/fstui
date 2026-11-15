// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (st *state) cloneOf(ops []string, now time.Time) error {
	if len(ops) != 2 {
		return fmt.Errorf("%w: clone needs a snapshot and a name", errUnsupported)
	}
	s, err := st.need(ops[0])
	if err != nil {
		return err
	}
	if _, ok := st.entries[ops[1]]; ok {
		return fmt.Errorf("cannot create '%s': dataset already exists", ops[1])
	}
	src := st.entries[datasetOf(ops[0])]
	c := src.clone(ops[1], src.Type)
	c.CreateTXG = st.nextTXG()
	for p, v := range map[string]string{
		"origin": ops[0], "used": "0", "createtxg": c.CreateTXG, "guid": st.nextGUID(),
		"creation": strconv.FormatInt(now.Unix(), 10), "usedbysnapshots": "0", "usedbychildren": "0", "usedbydataset": "0",
	} {
		c.set(p, v, srcNone)
	}
	st.inheritMountpoint(c)
	st.entries[ops[1]] = c
	s.set("clones", addListed(s.get("clones"), ops[1]), srcNone)
	return nil
}

func addListed(list, n string) string {
	if list == "" || list == "-" {
		return n
	}
	return list + "," + n
}

func removeListed(list, n string) string {
	parts := slices.DeleteFunc(strings.Split(list, ","), func(p string) bool { return p == n || p == "" })
	return strings.Join(parts, ",")
}

// inheritMountpoint gives a new dataset its parent's mountpoint plus its
// own name, as an inherited mountpoint is.
func (st *state) inheritMountpoint(e *entry) {
	i := strings.LastIndex(e.Name, "/")
	if i < 0 {
		return
	}
	parent, ok := st.entries[e.Name[:i]]
	if !ok {
		return
	}
	if mp := parent.get("mountpoint"); strings.HasPrefix(mp, "/") {
		e.set("mountpoint", strings.TrimSuffix(mp, "/")+"/"+e.Name[i+1:], srcInherited(e.Name[:i]))
	}
}

// rename moves an object and everything named below it.
func (st *state) rename(ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%w: rename needs two names", errUnsupported)
	}
	from, to := ops[0], ops[1]
	if _, err := st.need(from); err != nil {
		return err
	}
	if _, ok := st.entries[to]; ok {
		return fmt.Errorf("cannot rename to '%s': dataset already exists", to)
	}
	for _, n := range st.sorted() {
		if n != from && (isPoint(from) || !below(n, from)) {
			continue
		}
		e := st.entries[n]
		delete(st.entries, n)
		e.Name = to + strings.TrimPrefix(n, from)
		st.entries[e.Name] = e
		if h, ok := st.holds[n]; ok {
			delete(st.holds, n)
			st.holds[e.Name] = h
		}
	}
	if !isPoint(from) {
		st.inheritMountpoint(st.entries[to])
	}
	return nil
}

// rollback discards snapshots and bookmarks newer than snap (-r), or
// refuses as zfs does when there are any and -r was not given.
func (st *state) rollback(ops []string, recursive bool) error {
	if len(ops) != 1 {
		return fmt.Errorf("%w: rollback needs a snapshot", errUnsupported)
	}
	target, err := st.need(ops[0])
	if err != nil {
		return err
	}
	t, _ := strconv.ParseUint(target.CreateTXG, 10, 64)
	var newer []string
	for _, n := range st.sorted() {
		if isPoint(n) && datasetOf(n) == datasetOf(ops[0]) {
			if u, _ := strconv.ParseUint(st.entries[n].CreateTXG, 10, 64); u > t {
				newer = append(newer, n)
			}
		}
	}
	if len(newer) > 0 && !recursive {
		return fmt.Errorf("cannot rollback to '%s': more recent snapshots or bookmarks exist\nuse '-r' to force deletion of the following snapshots and bookmarks:\n%s", ops[0], strings.Join(newer, "\n"))
	}
	for _, n := range newer {
		st.remove(n)
	}
	return nil
}

// remove deletes one object and its holds, and unlinks a clone from
// its origin.
func (st *state) remove(n string) {
	if o := st.entries[n].get("origin"); o != "" && o != "-" {
		if s, ok := st.entries[o]; ok {
			s.set("clones", removeListed(s.get("clones"), n), srcNone)
		}
	}
	delete(st.entries, n)
	delete(st.holds, n)
}

func (st *state) destroy(ops []string, recursive bool) error {
	for _, op := range ops {
		names, _, err := st.destroySet(op, recursive)
		if err != nil {
			return err
		}
		for _, n := range names {
			if len(st.holds[n]) > 0 {
				return fmt.Errorf("cannot destroy snapshot %s: dataset is busy", n)
			}
		}
		for _, n := range names {
			st.remove(n)
		}
	}
	return nil
}

// promote moves the origin's snapshots up to the clone's origin to the
// clone, and makes the old origin dataset depend on the clone.
func (st *state) promote(ops []string) error {
	if len(ops) != 1 {
		return fmt.Errorf("%w: promote needs a dataset", errUnsupported)
	}
	clone, err := st.need(ops[0])
	if err != nil {
		return err
	}
	origin := clone.get("origin")
	o, ok := st.entries[origin]
	if !ok {
		return fmt.Errorf("cannot promote '%s': not a cloned filesystem", ops[0])
	}
	src := datasetOf(origin)
	ot, _ := strconv.ParseUint(o.CreateTXG, 10, 64)
	for _, n := range st.sorted() {
		if !strings.HasPrefix(n, src+"@") {
			continue
		}
		if t, _ := strconv.ParseUint(st.entries[n].CreateTXG, 10, 64); t > ot {
			continue
		}
		e := st.entries[n]
		delete(st.entries, n)
		e.Name = ops[0] + strings.TrimPrefix(n, src)
		st.entries[e.Name] = e
	}
	moved := ops[0] + strings.TrimPrefix(origin, src)
	st.entries[moved].set("clones", src, srcNone)
	clone.set("origin", "-", srcNone)
	st.entries[src].set("origin", moved, srcNone)
	return nil
}

func (st *state) property(set bool, ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%w: property change needs a property and a dataset", errUnsupported)
	}
	e, err := st.need(ops[1])
	if err != nil {
		return err
	}
	if set {
		k, v, _ := strings.Cut(ops[0], "=")
		e.set(k, v, srcLocal)
		return nil
	}
	k := ops[0]
	i := strings.LastIndex(ops[1], "/")
	if i < 0 {
		e.set(k, e.get(k), srcDefault)
		return nil
	}
	parent := st.entries[ops[1][:i]]
	from := parent.Name
	if s := string(parent.Properties[k].Source); strings.Contains(s, "INHERITED") {
		from = strings.Trim(strings.SplitN(s, `"data":`, 2)[1], `"}`)
	}
	e.set(k, parent.get(k), srcInherited(from))
	if k == "mountpoint" {
		st.inheritMountpoint(e)
	}
	return nil
}

func (st *state) create(ops, props []string, sparse bool, now time.Time) error {
	if len(ops) != 1 {
		return fmt.Errorf("%w: create needs one name", errUnsupported)
	}
	n := ops[0]
	i := strings.LastIndex(n, "/")
	if i < 0 {
		return fmt.Errorf("cannot create '%s': missing dataset name", n)
	}
	parent, err := st.need(n[:i])
	if err != nil {
		return fmt.Errorf("cannot create '%s': parent does not exist", n)
	}
	if _, ok := st.entries[n]; ok {
		return fmt.Errorf("cannot create '%s': dataset already exists", n)
	}
	e := parent.clone(n, "FILESYSTEM")
	e.CreateTXG = st.nextTXG()
	for k, p := range maps.Clone(e.Properties) {
		if strings.Contains(string(p.Source), "LOCAL") {
			e.set(k, p.Value, srcInherited(parent.Name))
		}
	}
	for p, v := range map[string]string{
		"used": "0", "referenced": "0", "usedbydataset": "0", "usedbysnapshots": "0", "usedbychildren": "0",
		"createtxg": e.CreateTXG, "guid": st.nextGUID(), "creation": strconv.FormatInt(now.Unix(), 10), "origin": "-",
		"mounted": "yes",
	} {
		e.set(p, v, srcNone)
	}
	st.inheritMountpoint(e)
	for _, kv := range props {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "-V":
			e.Type = "VOLUME"
			e.set("type", "volume", srcNone)
			e.set("volsize", v, srcLocal)
			e.set("volblocksize", "16384", srcDefault)
			e.set("mounted", "-", srcNone)
			delete(e.Properties, "mountpoint")
			if !sparse {
				e.set("refreservation", v, srcLocal)
			}
		case "-b":
			e.set("volblocksize", v, srcLocal)
		case "encryption":
			e.set("encryption", "aes-256-gcm", srcLocal)
			e.set("encryptionroot", n, srcNone)
			e.set("keystatus", "available", srcNone)
		default:
			e.set(k, v, srcLocal)
		}
	}
	st.entries[n] = e
	return nil
}

// key loads, unloads or changes the key of an encryption root. Any
// passphrase the planner accepts loads it: the demo has none to check.
func (st *state) key(cmd string, ops []string) error {
	if len(ops) != 1 {
		return fmt.Errorf("%w: %s needs one dataset", errUnsupported, cmd)
	}
	e, err := st.need(ops[0])
	if err != nil {
		return err
	}
	root, status := e.get("encryptionroot"), e.get("keystatus")
	switch {
	case root != ops[0]:
		return zfsSays("Key %s error: Keys must be unloaded for encryption root of '%s' (%s).", strings.TrimSuffix(cmd, "-key"), ops[0], root)
	case cmd == "load-key" && status == "available":
		return zfsSays("Key load error: Key already loaded for '%s'.", ops[0])
	case cmd != "load-key" && status != "available":
		return zfsSays("Key %s error: Key not loaded for '%s'.", strings.TrimSuffix(cmd, "-key"), ops[0])
	}
	for _, n := range st.sorted() {
		if below(n, ops[0]) && st.entries[n].get("encryptionroot") == ops[0] {
			switch cmd {
			case "load-key":
				st.entries[n].set("keystatus", "available", srcNone)
			case "unload-key":
				if st.entries[n].get("mounted") == "yes" {
					return zfsSays("Key unload error: '%s' is busy.", ops[0])
				}
				st.entries[n].set("keystatus", "unavailable", srcNone)
			}
		}
	}
	return nil
}

func (st *state) mount(mount bool, ops []string) error {
	if len(ops) != 1 {
		return fmt.Errorf("%w: mount needs one dataset", errUnsupported)
	}
	e, err := st.need(ops[0])
	if err != nil {
		return err
	}
	switch {
	case mount && e.get("mounted") == "yes":
		return fmt.Errorf("cannot mount '%s': filesystem already mounted", ops[0])
	case !mount && e.get("mounted") != "yes":
		return fmt.Errorf("cannot unmount '%s': not currently mounted", ops[0])
	}
	e.set("mounted", map[bool]string{true: "yes", false: "no"}[mount], srcNone)
	return nil
}

// zfsMessage is an error worded as zfs 2.3.9 words it, capitals and
// full stop included, for the demo to answer as zfs would.
type zfsMessage string

func (m zfsMessage) Error() string { return string(m) }

func zfsSays(format string, a ...any) error { return zfsMessage(fmt.Sprintf(format, a...)) }

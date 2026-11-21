// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Pool topology in the demo: devices attached, detached, replaced,
// added and removed, pools created, exported and imported. Each change
// is an edit kept with the pool and applied to its recorded zpool status
// every time it is read, as offline and online are, so the views see the
// pool as zpool would show it after the command.

// node is one vdev, or the pool itself, as zpool status -j prints it.
type node = map[string]any

// groups are where vdevs hang below the pool, and below each vdev.
var groups = []string{"vdevs", "special", "dedup", "logs", "l2cache", "spares"}

// classGroup is the pool-level group of each allocation class.
var classGroup = map[string]string{"normal": "vdevs", "special": "special", "dedup": "dedup", "log": "logs", "l2cache": "l2cache", "spare": "spares"}

// str is a string field of a vdev; "" when it has none.
func str(v node, field string) string {
	s, _ := v[field].(string)
	return s
}

func kids(v node, group string) node {
	m, _ := v[group].(node)
	return m
}

// root is the pool's root vdev, which holds the data vdevs.
func root(p node) node {
	for _, r := range kids(p, "vdevs") {
		if m, ok := r.(node); ok {
			return m
		}
	}
	return nil
}

// parentOf finds the vdev named name below v and returns the map that
// holds it and its key there.
func parentOf(v node, name string) (holder node, key string) {
	for _, g := range groups {
		for k, ch := range kids(v, g) {
			m, ok := ch.(node)
			if !ok {
				continue
			}
			if m["name"] == name {
				return kids(v, g), k
			}
			if h, kk := parentOf(m, name); h != nil {
				return h, kk
			}
		}
	}
	return nil, ""
}

// owner finds the vdev whose group holds the vdev named name.
func owner(v node, name string) node {
	for _, g := range groups {
		for _, ch := range kids(v, g) {
			m, ok := ch.(node)
			if !ok {
				continue
			}
			if m["name"] == name {
				return v
			}
			if o := owner(m, name); o != nil {
				return o
			}
		}
	}
	return nil
}

// nextIndex is the next top-level vdev number: zpool numbers mirror-N,
// raidz1-N and the like across every class.
func nextIndex(p node) int {
	n := 0
	var walk func(v node)
	walk = func(v node) {
		for _, g := range groups {
			for _, ch := range kids(v, g) {
				m, _ := ch.(node)
				name, _ := m["name"].(string)
				if i := strings.LastIndexByte(name, '-'); i > 0 && !strings.HasPrefix(name, "/") {
					if k, err := strconv.Atoi(name[i+1:]); err == nil {
						n = max(n, k+1)
					}
				}
				walk(m)
			}
		}
	}
	walk(p)
	return n
}

var demoGUID uint64 = 9_000_000_000_000_000_000

func newGUID() json.Number {
	demoGUID += 104729
	return json.Number(strconv.FormatUint(demoGUID, 10))
}

// leaf is a new device as zpool status shows it, ONLINE and healthy.
func leaf(path, class string) node {
	size := diskSize(path)
	return node{
		"name": path, "vdev_type": "disk", "guid": newGUID(), "path": path, "class": class, "state": "ONLINE",
		"rep_dev_size": size, "phys_space": size, "read_errors": 0, "write_errors": 0, "checksum_errors": 0,
	}
}

// group is a new mirror or raidz vdev of devs.
func group(typ string, index int, devs []string, class string) node {
	children := node{}
	for _, d := range devs {
		children[d] = leaf(d, class)
	}
	size := diskSize(devs[0])
	total := size * int64(len(devs))
	switch {
	case typ == "mirror":
		total = size
	case strings.HasPrefix(typ, "raidz"):
		parity, _ := strconv.Atoi(strings.TrimPrefix(typ, "raidz"))
		total = size * int64(len(devs)-max(parity, 1))
	}
	vtype, label := typ, typ
	if strings.HasPrefix(typ, "raidz") {
		vtype = "raidz"
	}
	if typ == "raidz" {
		label = "raidz1" // zpool's name for raidz without a number
	}
	return node{
		"name": fmt.Sprintf("%s-%d", label, index), "vdev_type": vtype, "guid": newGUID(), "class": class, "state": "ONLINE",
		"alloc_space": 0, "total_space": total, "def_space": total, "rep_dev_size": size,
		"read_errors": 0, "write_errors": 0, "checksum_errors": 0, "vdevs": children,
	}
}

// spec is a vdev specification as zpool create and add take it: groups
// of devices under mirror, raidzN, log, cache, special, spare and dedup.
type spec struct {
	class string // normal, log, l2cache, special, spare, dedup
	typ   string // mirror, raidz1..3, or "" for single devices
	devs  []string
}

func parseSpec(ops []string) []spec {
	var out []spec
	class := "normal"
	for _, op := range ops {
		switch op {
		case "log", "cache", "special", "spare", "dedup":
			class = map[string]string{"log": "log", "cache": "l2cache", "special": "special", "spare": "spare", "dedup": "dedup"}[op]
			out = append(out, spec{class: class})
		case "mirror", "raidz", "raidz1", "raidz2", "raidz3":
			out = append(out, spec{class: class, typ: op})
		default:
			if len(out) == 0 {
				out = append(out, spec{class: class})
			}
			last := &out[len(out)-1]
			if last.typ == "" && len(last.devs) > 0 {
				out = append(out, spec{class: class})
				last = &out[len(out)-1]
			}
			last.devs = append(last.devs, op)
		}
	}
	return slices.DeleteFunc(out, func(s spec) bool { return len(s.devs) == 0 })
}

// addSpec adds the vdevs of a specification to p.
func addSpec(p node, specs []spec) {
	for _, s := range specs {
		var holder node
		if s.class == "normal" {
			holder = kids(root(p), "vdevs")
		} else {
			g := classGroup[s.class]
			if kids(p, g) == nil {
				p[g] = node{}
			}
			holder = kids(p, g)
		}
		if s.typ == "" {
			for _, d := range s.devs {
				holder[d] = leaf(d, s.class)
			}
			continue
		}
		v := group(s.typ, nextIndex(p), s.devs, s.class)
		holder[str(v, "name")] = v
	}
}

// diskSize is a demo disk's size from its lsblk entry, matched by path,
// kernel name or WWN in the device path; 8 TB for one it does not know.
func diskSize(path string) int64 {
	var doc struct {
		Devices []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Size int64  `json:"size"`
			WWN  string `json:"wwn"`
		} `json:"blockdevices"`
	}
	if json.Unmarshal([]byte(demoDisks), &doc) == nil {
		for _, d := range doc.Devices {
			if path == d.Path || strings.HasSuffix(path, "/"+d.Name) || d.WWN != "" && strings.Contains(path, strings.TrimPrefix(d.WWN, "0x")) {
				return d.Size
			}
		}
	}
	return 8001563222016
}

// dataSize is what a pool can hold, from its data vdevs.
func dataSize(p node) int64 {
	var total int64
	for _, ch := range kids(root(p), "vdevs") {
		m, _ := ch.(node)
		if t, ok := num(m["total_space"]); ok {
			total += t
		} else if t, ok := num(m["rep_dev_size"]); ok {
			total += t
		}
	}
	return total
}

func num(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// applyTopology carries out the zpool commands that change which devices
// a pool has, or which pools there are. ok is false for other commands.
func (c *Commands) applyTopology(a execute.Argv) (res execute.Result, ok bool, err error) {
	if a.Tool != execute.Zpool || len(a.Options) == 0 {
		return res, false, nil
	}
	ops := a.Operands
	if _, err := c.load(); err != nil { // new and exported pools move datasets
		res, err = failed(a, err)
		return res, true, err
	}
	switch a.Options[0] {
	case "detach":
		err = c.edit(ops[0], func(p node) error { return detach(p, ops[1]) })
	case "attach":
		err = c.edit(ops[0], func(p node) error { return attach(p, ops[1], ops[2]) })
	case "replace":
		err = c.edit(ops[0], func(p node) error { return replace(p, ops[1], ops[2]) })
	case "add":
		specs := parseSpec(ops[1:])
		err = c.edit(ops[0], func(p node) error { addSpec(p, specs); return nil })
	case "remove":
		if slices.Contains(a.Options, "-s") {
			break // nothing is being removed in the demo: a removal finishes at once
		}
		err = c.edit(ops[0], func(p node) error { return remove(p, ops[1]) })
	case "upgrade":
	case "export":
		err = c.export(ops[0])
	case "import":
		err = c.importPool(a)
	case "create":
		err = c.create(ops[0], parseSpec(ops[1:]))
	default:
		return res, false, nil
	}
	if err != nil {
		res, err = failed(a, err)
		return res, true, err
	}
	return execute.Result{Duration: 40 * time.Millisecond}, true, nil
}

// edit records a change to a pool's devices; it is tried once now, on
// the pool as it is, so a change zpool would refuse fails here too.
func (c *Commands) edit(pool string, f func(node) error) error {
	p, err := c.currentStatus(pool)
	if err != nil {
		return err
	}
	if err := f(p); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ps := c.state(pool)
	ps.edits = append(ps.edits, func(p node) { _ = f(p) }) // checked above
	return nil
}

// state is a pool's changes. Called with c.mu held.
func (c *Commands) state(pool string) *poolState {
	if c.poolSt == nil {
		c.poolSt = map[string]*poolState{}
	}
	if c.poolSt[pool] == nil {
		c.poolSt[pool] = &poolState{}
	}
	return c.poolSt[pool]
}

func detach(p node, dev string) error {
	holder, key := parentOf(p, dev)
	if holder == nil {
		return fmt.Errorf("cannot detach %s: no such device in pool", dev)
	}
	mirror := owner(p, dev)
	delete(holder, key)
	// A mirror left with one side becomes that device, as in zpool.
	if mirror["vdev_type"] == "mirror" && len(holder) == 1 {
		up, upKey := parentOf(p, str(mirror, "name"))
		for k, last := range holder {
			delete(up, upKey)
			up[k] = last
		}
	}
	return nil
}

func attach(p node, target, dev string) error {
	holder, key := parentOf(p, target)
	if holder == nil {
		return fmt.Errorf("cannot attach %s to %s: no such device in pool", dev, target)
	}
	v, _ := holder[key].(node)
	switch v["vdev_type"] {
	case "raidz": // widening: the new device joins the vdev
		kids(v, "vdevs")[dev] = leaf(dev, str(v, "class"))
		if t, ok := num(v["total_space"]); ok {
			size, _ := num(v["rep_dev_size"])
			v["total_space"], v["def_space"] = t+size, t+size
		}
		return nil
	}
	if o := owner(p, target); o != nil && o["vdev_type"] == "mirror" {
		kids(o, "vdevs")[dev] = leaf(dev, str(v, "class"))
		return nil
	}
	m := group("mirror", nextIndex(p), []string{target}, str(v, "class"))
	kids(m, "vdevs")[target] = v
	kids(m, "vdevs")[dev] = leaf(dev, str(v, "class"))
	delete(holder, key)
	holder[str(m, "name")] = m
	return nil
}

func replace(p node, old, dev string) error {
	holder, key := parentOf(p, old)
	if holder == nil {
		return fmt.Errorf("cannot replace %s with %s: no such device in pool", old, dev)
	}
	v, _ := holder[key].(node)
	delete(holder, key)
	holder[dev] = leaf(dev, str(v, "class"))
	return nil
}

func remove(p node, target string) error {
	holder, key := parentOf(p, target)
	if holder == nil {
		return fmt.Errorf("cannot remove %s: no such device in pool", target)
	}
	delete(holder, key)
	return nil
}

// currentStatus is a pool's zpool status entry with the demo's changes,
// as a tree.
func (c *Commands) currentStatus(pool string) (node, error) {
	_, doc, err := c.poolDoc("zpool-status.json", pool)
	if err != nil {
		return nil, err
	}
	return decode(doc)
}

func decode(raw json.RawMessage) (node, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var p node
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("demo status: %w", err)
	}
	return p, nil
}

// export takes a pool and its datasets out of every listing; zpool
// import then lists it.
func (c *Commands) export(pool string) error {
	if !slices.Contains(c.poolNames(), pool) {
		return fmt.Errorf("cannot open '%s': no such pool", pool)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.away == nil {
		c.away = map[string]bool{}
	}
	c.away[pool] = true
	c.stash(pool)
	return nil
}

// stash moves a pool's datasets aside while it is exported, or back.
// Called with c.mu held.
func (c *Commands) stash(pool string) {
	if c.st == nil {
		return
	}
	if c.stashed == nil {
		c.stashed = map[string]map[string]*entry{}
	}
	if saved, ok := c.stashed[pool]; ok {
		maps.Copy(c.st.entries, saved)
		delete(c.stashed, pool)
		return
	}
	saved := map[string]*entry{}
	for n, e := range c.st.entries {
		if e.Pool == pool {
			saved[n] = e
			delete(c.st.entries, n)
		}
	}
	c.stashed[pool] = saved
}

// importPool brings back an exported pool, or the made-up machine's old
// pool, by name or numeric id, optionally under a new name, read-only or
// rewound to its checkpoint.
func (c *Commands) importPool(a execute.Argv) error {
	if len(a.Operands) == 0 {
		return fmt.Errorf("%w: %s", errUnsupported, a)
	}
	want, as := a.Operands[0], ""
	if len(a.Operands) > 1 {
		as = a.Operands[1]
	}
	pool, err := c.exported(want)
	if err != nil {
		return err
	}
	if pool != "" {
		if as != "" && as != pool {
			return fmt.Errorf("the demo imports %s under its own name only", pool)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.away, pool)
		c.stash(pool)
		ps := c.state(pool)
		ps.readonly = slices.Contains(a.Options, "readonly=on")
		// A read-only rewind is a look: the checkpoint stays, and an
		// import without it returns to the present (zpool-import(8)).
		if slices.Contains(a.Options, "--rewind-to-checkpoint") && !ps.readonly {
			ps.checkpoint = false
		}
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if (want != oldID && want != oldName) || c.oldImported {
		return fmt.Errorf("cannot import '%s': no such pool available", want)
	}
	if as == "" {
		as = oldName
	}
	c.oldImported = true
	return c.make(as, []spec{{class: "normal", typ: "mirror", devs: []string{oldDevices[0], oldDevices[1]}}}, func(p node) {
		holder, k := parentOf(p, oldDevices[1])
		if m, ok := holder[k].(node); ok {
			m["state"] = "UNAVAIL"
		}
		p["state"] = "DEGRADED"
	})
}

// exported is the exported pool named want, or with want as its id; ""
// when there is none.
func (c *Commands) exported(want string) (string, error) {
	c.mu.Lock()
	away := slices.Sorted(maps.Keys(c.away))
	c.mu.Unlock()
	for _, pool := range away {
		p, err := c.currentStatus(pool)
		if err != nil {
			return "", err
		}
		if want == pool || fmt.Sprint(p["pool_guid"]) == want {
			return pool, nil
		}
	}
	return "", nil
}

// oldDevices are the disks of the made-up old pool, one of them gone.
var oldDevices = [2]string{"wwn-0x5000c500demo0l", "wwn-0x5000c500demo0m"}

// The made-up machine's importable pool (demoImportable).
const (
	oldName = "oldtank"
	oldID   = "4866379416960248110"
)

// create makes a new pool on the given devices.
func (c *Commands) create(name string, specs []spec) error {
	if slices.Contains(c.poolNames(), name) {
		return fmt.Errorf("cannot create '%s': pool already exists", name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.make(name, specs, nil)
}

// make adds a pool built like the recorded healthy one, with the devices
// of specs and an empty root filesystem. Called with c.mu held.
func (c *Commands) make(name string, specs []spec, then func(node)) error {
	c.made = append(c.made, name)
	ps := c.state(name)
	ps.guid = newGUID()
	ps.edits = append(ps.edits, func(p node) {
		p["name"], p["pool_guid"] = name, ps.guid
		r := root(p)
		r["name"], r["guid"], r["vdevs"] = name, ps.guid, node{}
		if rs := kids(p, "vdevs"); rs != nil {
			for k := range rs {
				delete(rs, k)
			}
			rs[name] = r
		}
		for _, g := range groups[1:] {
			delete(p, g)
		}
		addSpec(p, specs)
		p["error_count"] = 0
		delete(p, "scan_stats")
		if then != nil {
			then(p)
		}
	})
	if c.st != nil {
		if tmpl, ok := c.st.entries[template]; ok {
			e := tmpl.clone(name, "FILESYSTEM")
			e.Pool = name
			e.CreateTXG = c.st.nextTXG()
			e.set("mountpoint", "/"+name, srcDefault)
			for _, p := range []string{"used", "referenced", "usedbydataset", "logicalused", "logicalreferenced", "written"} {
				e.set(p, "98304", srcNone)
			}
			e.set("guid", c.st.nextGUID(), srcNone)
			c.st.entries[name] = e
		}
	}
	return nil
}

// template is the recorded pool a new one is modelled on.
const template = "tank"

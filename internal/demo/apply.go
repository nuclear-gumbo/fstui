// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Apply carries out a change on the demo's state instead of running
// zfs: the demo's write mode uses it in place of starting a process. It
// understands the commands fstui's changes produce and refuses others.
func (c *Commands) Apply(a execute.Argv, stdin []byte) (execute.Result, error) {
	switch a.Tool { //nolint:exhaustive // zfs changes below
	case execute.Zpool:
		return c.applyPool(a)
	case execute.Tee, execute.Cp, execute.Mv:
		return c.applyFile(a, stdin)
	}
	st, err := c.load()
	if err != nil {
		return execute.Result{ExitCode: 1}, err
	}
	c.mu.Lock()
	err = st.apply(a, c.now())
	c.mu.Unlock()
	if err != nil {
		msg := termtext.New(err.Error() + "\n")
		return execute.Result{ExitCode: 1, Stderr: msg}, &execute.CommandError{Argv: a, ExitCode: 1, Stderr: msg}
	}
	return execute.Result{Duration: 3 * time.Millisecond}, nil
}

func (c *Commands) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// errUnsupported is returned for a change the demo does not simulate.
var errUnsupported = errors.New("the demo does not simulate this command")

// flags splits the options after the subcommand into flags, -o values,
// and the values of -V (volume size) and -b (block size), kept as
// "-V=size" and "-b=size" in props.
func flags(opts []string) (set map[string]bool, props []string) {
	set = map[string]bool{}
	for i := 1; i < len(opts); i++ {
		switch o := opts[i]; {
		case o == "-o" && i+1 < len(opts):
			props = append(props, opts[i+1])
			i++
		case (o == "-V" || o == "-b") && i+1 < len(opts):
			props = append(props, o+"="+opts[i+1])
			i++
		default:
			set[o] = true
		}
	}
	return set, props
}

func (st *state) apply(a execute.Argv, now time.Time) error {
	if a.Tool != execute.ZFS || len(a.Options) == 0 {
		return fmt.Errorf("%w: %s", errUnsupported, a)
	}
	f, props := flags(a.Options)
	ops := a.Operands
	switch a.Options[0] {
	case "snapshot":
		return st.snapshot(ops, f["-r"], now)
	case "hold", "release":
		return st.hold(a.Options[0] == "hold", ops, f["-r"])
	case "bookmark":
		return st.bookmark(ops)
	case "clone":
		return st.cloneOf(ops, now)
	case "rename":
		return st.rename(ops)
	case "rollback":
		return st.rollback(ops, f["-r"])
	case "destroy":
		return st.destroy(ops, f["-r"])
	case "promote":
		return st.promote(ops)
	case "set", "inherit":
		return st.property(a.Options[0] == "set", ops)
	case "create":
		return st.create(ops, props, f["-s"], now)
	case "mount", "unmount":
		return st.mount(a.Options[0] == "mount", ops)
	case "load-key", "unload-key", "change-key":
		return st.key(a.Options[0], ops)
	}
	return fmt.Errorf("%w: %s", errUnsupported, a)
}

func (st *state) need(n string) (*entry, error) {
	e, ok := st.entries[n]
	if !ok {
		return nil, fmt.Errorf("cannot open '%s': %s", n, missingWhy(n))
	}
	return e, nil
}

func (st *state) snapshot(ops []string, recursive bool, now time.Time) error {
	var todo []string
	for _, op := range ops {
		ds, short, _ := strings.Cut(op, "@")
		if _, err := st.need(ds); err != nil {
			return err
		}
		for _, n := range st.sorted() {
			if n == ds || recursive && below(n, ds) && !isPoint(n) {
				todo = append(todo, n+"@"+short)
			}
		}
	}
	for _, n := range todo {
		if _, ok := st.entries[n]; ok {
			return fmt.Errorf("cannot create snapshot '%s': dataset already exists", n)
		}
	}
	txg := st.nextTXG()
	for _, n := range todo {
		parent := st.entries[datasetOf(n)]
		s := parent.clone(n, "SNAPSHOT")
		s.CreateTXG = txg
		for p, v := range map[string]string{
			"used": "0", "creation": strconv.FormatInt(now.Unix(), 10), "createtxg": txg, "guid": st.nextGUID(),
			"userrefs": "0", "clones": "", "written": "0",
		} {
			s.set(p, v, srcNone)
		}
		st.entries[n] = s
	}
	return nil
}

func (st *state) hold(add bool, ops []string, recursive bool) error {
	if len(ops) < 2 {
		return fmt.Errorf("%w: hold without a tag", errUnsupported)
	}
	tag := ops[0]
	for _, snap := range ops[1:] {
		for _, t := range st.sameSnapshots(snap, recursive) {
			if err := st.holdOne(add, t, tag); err != nil {
				return err
			}
		}
	}
	return nil
}

func (st *state) holdOne(add bool, snap, tag string) error {
	e, err := st.need(snap)
	if err != nil {
		return err
	}
	has := slices.Contains(st.holds[snap], tag)
	switch {
	case add && has:
		return fmt.Errorf("cannot hold snapshot '%s': tag already exists on this dataset", snap)
	case !add && !has:
		return fmt.Errorf("cannot release hold from snapshot '%s': no such tag on this dataset", snap)
	case add:
		st.holds[snap] = append(st.holds[snap], tag)
	default:
		st.holds[snap] = slices.DeleteFunc(st.holds[snap], func(t string) bool { return t == tag })
	}
	e.set("userrefs", strconv.Itoa(len(st.holds[snap])), srcNone)
	return nil
}

func (st *state) bookmark(ops []string) error {
	if len(ops) != 2 {
		return fmt.Errorf("%w: bookmark needs a snapshot and a name", errUnsupported)
	}
	s, err := st.need(ops[0])
	if err != nil {
		return err
	}
	if _, ok := st.entries[ops[1]]; ok {
		return fmt.Errorf("cannot create bookmark '%s': bookmark exists", ops[1])
	}
	b := &entry{Name: ops[1], Type: "BOOKMARK", Pool: s.Pool, CreateTXG: s.CreateTXG, Properties: map[string]prop{}}
	for _, p := range []string{"createtxg", "creation", "guid", "referenced", "logicalreferenced"} {
		b.Properties[p] = s.Properties[p]
	}
	b.set("type", "bookmark", srcNone)
	st.entries[ops[1]] = b
	return nil
}

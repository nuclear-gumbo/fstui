// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// The reads change plans make (internal/zfs/plan) use zfs's tab output.
// They are answered from the demo's state as zfs 2.3.9 answers them,
// including the partial output and message for a name that does not
// exist.

// partialError is a command that printed something and then failed, as
// zfs list does for a mix of existing and missing names.
type partialError struct {
	out    []byte
	stderr string
}

func (e *partialError) Error() string { return strings.TrimSpace(e.stderr) }

// textAnswer answers the tab-output commands, or reports false.
func (c *Commands) textAnswer(a execute.Argv) ([]byte, bool, error) {
	cmd := string(a.Tool) + " " + strings.Join(a.Options, " ")
	switch cmd {
	case "zfs list -H -p -t all -o name,guid,createtxg":
		return c.rows(a.Operands, false, func(e *entry) []string { return []string{e.Name, e.get("guid"), e.CreateTXG} })
	case "zfs list -H -r -t filesystem,volume -o name":
		return c.descendants(a.Operands)
	case "zfs list -H -p -d 1 -t snapshot,bookmark -o name,createtxg,clones -s createtxg":
		return c.points(a.Operands)
	case "zfs holds -H -p":
		return c.holdRows(a.Operands)
	}
	switch {
	case strings.HasPrefix(cmd, "zfs get -H -p -o name,property,value "):
		props := strings.Split(a.Options[len(a.Options)-1], ",")
		return c.rows(a.Operands, true, func(e *entry) []string {
			var out []string
			for _, p := range props {
				v := "-"
				if pv, ok := e.Properties[p]; ok {
					v = pv.Value
				}
				out = append(out, e.Name+"\t"+p+"\t"+v)
			}
			return out
		})
	case strings.HasPrefix(cmd, "zfs destroy -n -v -p"):
		return c.dryDestroy(a)
	case strings.HasPrefix(cmd, "zfs send -n -P"):
		return c.drySend(a)
	}
	return nil, false, nil
}

// drySend answers zfs send -n -P from the demo's state, in zfs's format:
// a full stream is the snapshot's logical size, or with -c or -w the
// smaller of that and its size on disk; an incremental stream is what
// each later snapshot wrote, scaled up by its compression ratio unless -c
// or -w keep it compressed.
func (c *Commands) drySend(a execute.Argv) ([]byte, bool, error) {
	st, err := c.load()
	if err != nil || len(a.Operands) != 1 {
		return nil, true, err
	}
	o := sendArgs(a.Options)
	c.mu.Lock()
	defer c.mu.Unlock()
	to, ok := st.entries[a.Operands[0]]
	if !ok {
		return nil, true, &partialError{stderr: fmt.Sprintf("cannot open '%s': %s\n", a.Operands[0], missingWhy(a.Operands[0]))}
	}
	var out strings.Builder
	var total uint64
	if o.from == "" {
		total = o.size(to, "logicalreferenced")
		if o.compressed {
			// The recordings' tiny datasets take more on disk than their
			// logical size (metadata); a compressed stream is never larger.
			total = min(total, o.size(to, "referenced"))
		}
		fmt.Fprintf(&out, "full\t%s\t%d\n", to.Name, total)
	} else {
		total, err = st.sendIncremental(&out, o, to)
		if err != nil {
			return nil, true, err
		}
	}
	fmt.Fprintf(&out, "size\t%d\n", total)
	return []byte(out.String()), true, nil
}

// sendOpts are the zfs send options the demo's estimate depends on.
type sendOpts struct {
	compressed bool   // -c or -w
	from       string // -i or -I
	every      bool   // -I
}

func sendArgs(opts []string) sendOpts {
	var o sendOpts
	for i := 0; i < len(opts); i++ {
		switch opts[i] {
		case "-c", "-w":
			o.compressed = true
		case "-i", "-I":
			if i+1 < len(opts) {
				o.from, o.every = opts[i+1], opts[i] == "-I"
				i++
			}
		}
	}
	return o
}

// size is a byte property of e as the stream would carry it.
func (o sendOpts) size(e *entry, prop string) uint64 {
	n, _ := strconv.ParseUint(e.get(prop), 10, 64)
	if !o.compressed && prop == "written" {
		if r, err := strconv.ParseFloat(strings.TrimSuffix(e.get("compressratio"), "x"), 64); err == nil && r > 1 {
			n = uint64(float64(n) * r)
		}
	}
	return n
}

// sendIncremental writes the incremental lines from o.from to to and
// returns their total.
func (st *state) sendIncremental(out *strings.Builder, o sendOpts, to *entry) (uint64, error) {
	ds, _, _ := strings.Cut(to.Name, "@")
	from := o.from
	if strings.HasPrefix(from, "@") || strings.HasPrefix(from, "#") {
		from = ds + from
	}
	start, ok := st.entries[from]
	if !ok {
		return 0, &partialError{stderr: fmt.Sprintf("cannot open '%s': %s\n", from, missingWhy(from))}
	}
	lo, _ := strconv.ParseUint(start.CreateTXG, 10, 64)
	hi, _ := strconv.ParseUint(to.CreateTXG, 10, 64)
	prev, sum := from, uint64(0)
	for _, n := range st.sorted() {
		e := st.entries[n]
		txg, _ := strconv.ParseUint(e.CreateTXG, 10, 64)
		if e.Type != "SNAPSHOT" || !strings.HasPrefix(n, ds+"@") || txg <= lo || txg > hi {
			continue
		}
		sum += o.size(e, "written")
		if o.every {
			_, short, _ := strings.Cut(prev, "@")
			fmt.Fprintf(out, "incremental\t%s\t%s\t%d\n", short, n, o.size(e, "written"))
			prev = n
		}
	}
	if !o.every {
		fmt.Fprintf(out, "incremental\t%s\t%s\t%d\n", from, to.Name, sum)
	}
	return sum, nil
}

// rows prints one line per named object, in operand order; lines holds
// whole lines when lines is set, fields to join with tabs otherwise.
// Missing names fail as zfs does, after the others are printed.
func (c *Commands) rows(names []string, lines bool, f func(*entry) []string) ([]byte, bool, error) {
	st, err := c.load()
	if err != nil {
		return nil, true, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out, stderr strings.Builder
	for _, n := range names {
		e, ok := st.entries[n]
		if !ok {
			fmt.Fprintf(&stderr, "cannot open '%s': %s\n", n, missingWhy(n))
			continue
		}
		if lines {
			for _, l := range f(e) {
				out.WriteString(l + "\n")
			}
		} else {
			out.WriteString(strings.Join(f(e), "\t") + "\n")
		}
	}
	if stderr.Len() > 0 {
		return nil, true, &partialError{out: []byte(out.String()), stderr: stderr.String()}
	}
	return []byte(out.String()), true, nil
}

// missingWhy is zfs's reason for a name that does not exist (2.3.9).
func missingWhy(n string) string {
	if strings.Contains(n, "#") {
		return "no such pool or dataset"
	}
	return "dataset does not exist"
}

func (c *Commands) descendants(ops []string) ([]byte, bool, error) {
	st, err := c.load()
	if err != nil || len(ops) != 1 {
		return nil, true, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := st.entries[ops[0]]; !ok {
		return nil, true, fmt.Errorf("cannot open '%s': dataset does not exist", ops[0])
	}
	var out strings.Builder
	for _, n := range st.sorted() {
		if below(n, ops[0]) && !isPoint(n) {
			out.WriteString(n + "\n")
		}
	}
	return []byte(out.String()), true, nil
}

func (c *Commands) points(ops []string) ([]byte, bool, error) {
	st, err := c.load()
	if err != nil || len(ops) != 1 {
		return nil, true, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out strings.Builder
	for _, n := range st.sorted() {
		if isPoint(n) && datasetOf(n) == ops[0] {
			e := st.entries[n]
			clones := e.get("clones")
			if clones == "" || e.Type == "BOOKMARK" {
				clones = "-"
			}
			fmt.Fprintf(&out, "%s\t%s\t%s\n", n, e.CreateTXG, clones)
		}
	}
	return []byte(out.String()), true, nil
}

func (c *Commands) holdRows(snaps []string) ([]byte, bool, error) {
	st, err := c.load()
	if err != nil {
		return nil, true, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out strings.Builder
	for _, s := range snaps {
		for _, tag := range st.holds[s] {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", s, tag, "1794548467")
		}
	}
	return []byte(out.String()), true, nil
}

// dryDestroy answers zfs destroy -nvp: what would go, and for
// snapshots the space they hold. Like zfs, it refuses a dataset with
// children or snapshots without -r, and anything with clones.
func (c *Commands) dryDestroy(a execute.Argv) ([]byte, bool, error) {
	st, err := c.load()
	if err != nil || len(a.Operands) != 1 {
		return nil, true, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	names, reclaim, err := st.destroySet(a.Operands[0], slices.Contains(a.Options, "-r"))
	if err != nil {
		return nil, true, err
	}
	var out strings.Builder
	for _, n := range names {
		out.WriteString("destroy\t" + n + "\n")
	}
	if strings.Contains(a.Operands[0], "@") {
		out.WriteString("reclaim\t" + strconv.FormatUint(reclaim, 10) + "\n")
	}
	return []byte(out.String()), true, nil
}

// destroySet is what destroying target removes, children first, with
// the space its snapshots hold; or zfs's refusal.
func (st *state) destroySet(target string, recursive bool) ([]string, uint64, error) {
	e, ok := st.entries[target]
	if !ok {
		return nil, 0, fmt.Errorf("cannot open '%s': %s", target, missingWhy(target))
	}
	var names []string
	var err error
	if e.Type == "SNAPSHOT" {
		names = st.sameSnapshots(target, recursive)
	} else if names, err = st.datasetSet(target, recursive); err != nil {
		return nil, 0, err
	}
	var reclaim uint64
	for _, n := range names {
		if c := st.entries[n].get("clones"); c != "" && c != "-" {
			return nil, 0, fmt.Errorf("cannot destroy '%s': snapshot has dependent clones\nuse '-R' to destroy the following datasets:\n%s", n, c)
		}
		if strings.Contains(n, "@") {
			u, _ := strconv.ParseUint(st.entries[n].get("used"), 10, 64)
			reclaim += u
		}
	}
	return names, reclaim, nil
}

// sameSnapshots is snap, and with recursive the snapshots of the same
// name below its dataset.
func (st *state) sameSnapshots(snap string, recursive bool) []string {
	names := []string{snap}
	if !recursive {
		return names
	}
	_, short, _ := strings.Cut(snap, "@")
	for _, n := range st.sorted() {
		if n != snap && strings.HasSuffix(n, "@"+short) && below(n, datasetOf(snap)) {
			names = append(names, n)
		}
	}
	return names
}

// datasetSet is a dataset and, with recursive, everything below it,
// deepest first; without recursive, anything below is zfs's refusal.
func (st *state) datasetSet(ds string, recursive bool) ([]string, error) {
	var names []string
	child, point := "", ""
	for _, n := range st.sorted() {
		if n == ds || !below(n, ds) {
			continue
		}
		switch {
		case isPoint(n) && point == "":
			point = n
		case !isPoint(n) && child == "":
			child = n
		}
		names = append(names, n)
	}
	if !recursive && len(names) > 0 {
		what, first := "children", child
		if child == "" {
			what, first = "snapshots", point
		}
		return nil, fmt.Errorf("cannot destroy '%s': filesystem has %s\nuse '-r' to destroy the following datasets:\n%s", ds, what, first)
	}
	slices.Reverse(names)
	return append(names, ds), nil
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo

package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// The demo's datasets, snapshots and bookmarks live in memory, loaded
// once from the recordings. Reads answer from here, and changes made in
// the demo's write mode change it, so a snapshot taken in the demo
// appears in its lists. Nothing is ever written to disk or run.

// prop is one property as zfs get -j -p prints it.
type prop struct {
	Value  string          `json:"value"`
	Source json.RawMessage `json:"source"`
}

// entry is one dataset, snapshot or bookmark.
type entry struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"` // FILESYSTEM, VOLUME, SNAPSHOT, BOOKMARK
	Pool       string          `json:"pool"`
	CreateTXG  string          `json:"createtxg"`
	Properties map[string]prop `json:"properties"`
}

func (e *entry) get(p string) string { return e.Properties[p].Value }

func (e *entry) set(p, v string, source json.RawMessage) {
	e.Properties[p] = prop{Value: v, Source: source}
}

var (
	srcNone    = json.RawMessage(`{"type":"NONE","data":"-"}`)
	srcLocal   = json.RawMessage(`{"type":"LOCAL","data":"-"}`)
	srcDefault = json.RawMessage(`{"type":"DEFAULT","data":"-"}`)
)

func srcInherited(from string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"type": "INHERITED", "data": from}) //nolint:errchkjson // a map of strings always encodes
	return b
}

// state is the demo's system.
type state struct {
	entries map[string]*entry
	holds   map[string][]string // snapshot -> tags, in order
	txg     uint64              // the last transaction group used
	guid    uint64              // for new objects
}

// load reads every scenario's recordings into memory once.
func (c *Commands) load() (*state, error) {
	c.mu.Lock()
	st := c.st
	c.mu.Unlock()
	if st != nil {
		return st, nil
	}
	st = &state{entries: map[string]*entry{}, holds: map[string][]string{}, guid: 1 << 40}
	for _, p := range pools {
		if err := c.loadPool(st, p.dir); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.st == nil {
		c.st = st
	}
	return c.st, nil
}

func (c *Commands) loadPool(st *state, dir string) error {
	b, err := c.file(dir, "zfs-get-p.json")
	if err != nil {
		return err
	}
	var doc struct {
		Datasets map[string]*entry `json:"datasets"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("demo recording %s/zfs-get-p.json: %w", dir, err)
	}
	for name, e := range doc.Datasets {
		st.entries[name] = e
		if n, err := strconv.ParseUint(e.CreateTXG, 10, 64); err == nil {
			st.txg = max(st.txg, n)
		}
	}
	holds, err := c.file(dir, "zfs-holds.txt")
	if err != nil {
		return nil //nolint:nilerr // not every scenario recorded holds
	}
	for line := range bytes.Lines(holds) {
		f := strings.Split(strings.TrimRight(string(line), "\n"), "\t")
		if len(f) >= 2 {
			st.holds[f[0]] = append(st.holds[f[0]], f[1])
		}
	}
	return nil
}

// sorted returns entry names in the order zfs lists them: by name, with
// a dataset's snapshots and bookmarks right after it, oldest first.
func (st *state) sorted() []string {
	names := slices.Collect(maps.Keys(st.entries))
	key := func(n string) (string, uint64) {
		ds := datasetOf(n)
		t, _ := strconv.ParseUint(st.entries[n].CreateTXG, 10, 64)
		if ds == n {
			t = 0
		}
		return ds, t
	}
	slices.SortFunc(names, func(a, b string) int {
		da, ta := key(a)
		db, tb := key(b)
		if c := strings.Compare(da, db); c != 0 {
			return c
		}
		if ta != tb {
			if ta < tb {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	return names
}

// below reports whether n is ds itself, or a dataset, snapshot or
// bookmark below it.
func below(n, ds string) bool {
	return n == ds || strings.HasPrefix(n, ds+"/") || strings.HasPrefix(n, ds+"@") || strings.HasPrefix(n, ds+"#")
}

// isPoint reports whether n is a snapshot or bookmark.
func isPoint(n string) bool { return strings.ContainsAny(n, "@#") }

// datasetOf returns the dataset of a snapshot or bookmark name, or n.
func datasetOf(n string) string {
	ds, _, _ := strings.Cut(strings.Replace(n, "#", "@", 1), "@")
	return ds
}

// nextTXG returns a new transaction group for something created now.
func (st *state) nextTXG() string {
	st.txg++
	return strconv.FormatUint(st.txg, 10)
}

func (st *state) nextGUID() string {
	st.guid += 7919
	return strconv.FormatUint(st.guid, 10)
}

// clone copies e's properties under a new name and type.
func (e *entry) clone(name, typ string) *entry {
	n := &entry{Name: name, Type: typ, Pool: e.Pool, CreateTXG: e.CreateTXG, Properties: maps.Clone(e.Properties)}
	n.set("type", strings.ToLower(typ), srcNone)
	return n
}

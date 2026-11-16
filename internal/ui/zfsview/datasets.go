// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/nuclear-gumbo/fstui/internal/ui"
	"github.com/nuclear-gumbo/fstui/internal/ui/keys"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// DatasetSource reads datasets. *zfs.Reader implements it.
type DatasetSource interface {
	Datasets(ctx context.Context) ([]zfs.Dataset, error)
	DatasetProperties(ctx context.Context, ds name.Dataset) (zfs.Dataset, error)
	DatasetSnapshots(ctx context.Context, ds name.Dataset) ([]zfs.Dataset, error)
	Allow(ctx context.Context, ds name.Dataset) ([]zfs.Delegation, error)
}

// DatasetsOptions configure the Datasets view.
type DatasetsOptions struct {
	Interval time.Duration
	Now      func() time.Time
}

// Datasets is the tree of filesystems and volumes in every pool (view 2).
type Datasets struct {
	src       DatasetSource
	opts      DatasetsOptions
	poll      poller[[]zfs.Dataset]
	selected  string          // dataset name under the cursor, kept across refreshes
	marked    marks           // marked for one change to several (m)
	collapsed map[string]bool // collapsed nodes, by name
	first     int             // row shown at the top by the last render
	detail    *dsDetail       // the dataset whose detail is shown, if any
	focus     bool            // keys go to the detail rather than the tree
	geo       geometry        // layout of the last render, for the mouse
	tree      derived[listKey, []*node]
}

// NewDatasets returns the Datasets view reading from src.
func NewDatasets(src DatasetSource, o DatasetsOptions) *Datasets {
	return &Datasets{
		src: src, opts: o, collapsed: map[string]bool{}, marked: marks{},
		poll: poller[[]zfs.Dataset]{read: src.Datasets, interval: o.Interval, now: o.Now},
	}
}

// node is a dataset in the tree.
type node struct {
	d        zfs.Dataset
	depth    int
	children []*node
}

// tree builds the hierarchy from names. zfs list sorts by name, which
// does not keep children together ("a-b" sorts between "a" and "a/b"), so
// parents are found by name rather than by position.
func tree(list []zfs.Dataset) []*node {
	byName := make(map[string]*node, len(list))
	for _, d := range list {
		byName[d.Name] = &node{d: d}
	}
	var roots []*node
	for _, d := range list {
		n := byName[d.Name]
		i := strings.LastIndexByte(d.Name, '/')
		if parent, ok := byName[d.Name[:max(i, 0)]]; i >= 0 && ok {
			parent.children = append(parent.children, n)
		} else {
			roots = append(roots, n)
		}
	}
	var setDepth func(ns []*node, depth int)
	setDepth = func(ns []*node, depth int) {
		sort.Slice(ns, func(a, b int) bool { return ns[a].d.Name < ns[b].d.Name })
		for _, n := range ns {
			n.depth = depth
			setDepth(n.children, depth+1)
		}
	}
	setDepth(roots, 0)
	return roots
}

// rows lists the visible nodes in tree order, skipping the children of
// collapsed nodes.
func (v *Datasets) rows() []*node {
	var out []*node
	var walk func(ns []*node)
	walk = func(ns []*node) {
		for _, n := range ns {
			out = append(out, n)
			if !v.collapsed[n.d.Name] {
				walk(n.children)
			}
		}
	}
	walk(v.tree.get(listKey{loaded: v.poll.loaded, n: len(v.poll.data)}, func() []*node { return tree(v.poll.data) }))
	return out
}

func (v *Datasets) cursor(rows []*node) int {
	for i, n := range rows {
		if n.d.Name == v.selected {
			return i
		}
	}
	return 0
}

// Name implements ui.Screen.
func (v *Datasets) Name() string { return "Datasets" }

// Framed implements ui.Framed: the tree and the selected dataset are
// drawn side by side from splitWidth columns.
func (v *Datasets) Framed() bool { return true }

// Number implements ui.Numbered.
func (v *Datasets) Number() int { return datasetsView }

// Keys implements ui.Screen.
func (v *Datasets) Keys() []key.Binding {
	if v.focus {
		return v.detail.bindings()
	}
	return []key.Binding{keys.As(keys.Select, "open"), keys.As(keys.Down, "next"), keys.As(keys.Up, "previous"), keys.Get(keys.Mark), keys.Get(keys.ClearMarks), keys.As(keys.Left, "collapse"), keys.As(keys.Right, "expand"), keys.Get(keys.Refresh), keys.Get(keys.First), keys.Get(keys.Last)}
}

// Nested implements ui.Screen.
func (v *Datasets) Nested() bool { return v.focus }

// Init starts the first load.
func (v *Datasets) Init() tea.Cmd { return v.poll.start() }

// Update implements ui.Screen.
func (v *Datasets) Update(msg tea.Msg) (ui.Screen, tea.Cmd) {
	if cmd, updated := v.poll.handle(msg); cmd != nil || updated {
		v.marked.keep(func(n string) bool {
			return slices.ContainsFunc(v.poll.data, func(d zfs.Dataset) bool { return d.Name == n })
		})
		if v.geo.split {
			cmd = tea.Batch(cmd, v.follow())
		}
		return v, cmd
	}
	if v.detail != nil {
		if cmd, handled := v.detail.handle(msg); handled {
			return v, cmd
		}
	}
	if cmd, ok := v.mouse(msg); ok {
		return v, cmd
	}
	if c, ok := msg.(ui.ConfigMsg); ok {
		v.opts.Interval, v.poll.interval = c.Config.Refresh.Datasets, c.Config.Refresh.Datasets
		return v, nil
	}
	if _, ok := msg.(ui.ChangedMsg); ok {
		clear(v.marked) // what they named may be gone
		return v, v.changed()
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		return v, v.keyPress(k)
	}
	return v, nil
}

// keyPress sends a key to the detail when it has the focus, otherwise to
// the tree, whose selection the detail follows in the split layout.
func (v *Datasets) keyPress(k tea.KeyPressMsg) tea.Cmd {
	if v.focus {
		if key.Matches(k, keys.As(keys.Back, "back")) {
			v.focus = false
			if !v.geo.split {
				v.detail = nil
			}
			return nil
		}
		return tea.Batch(v.detail.init(), v.detail.key(k, v.poll.refresh))
	}
	cmd := v.key(k)
	if v.geo.split && !v.focus {
		cmd = tea.Batch(cmd, v.follow())
	}
	return cmd
}

func (v *Datasets) key(msg tea.KeyPressMsg) tea.Cmd {
	rows := v.rows()
	if len(rows) == 0 {
		if key.Matches(msg, keys.Get(keys.Refresh)) {
			return v.poll.refresh()
		}
		return nil
	}
	i := v.cursor(rows)
	cur := rows[i]
	switch {
	case key.Matches(msg, keys.As(keys.Down, "next")):
		i = min(i+1, len(rows)-1)
	case key.Matches(msg, keys.As(keys.Up, "previous")):
		i = max(i-1, 0)
	case key.Matches(msg, keys.Get(keys.First)):
		i = 0
	case key.Matches(msg, keys.Get(keys.Last)):
		i = len(rows) - 1
	case key.Matches(msg, keys.As(keys.Left, "collapse")):
		if len(cur.children) > 0 && !v.collapsed[cur.d.Name] {
			v.collapsed[cur.d.Name] = true
		} else if j := parentRow(rows, i); j >= 0 {
			i = j // on a leaf or a collapsed node, h moves to the parent
		}
	case key.Matches(msg, keys.As(keys.Right, "expand")):
		delete(v.collapsed, cur.d.Name)
	case key.Matches(msg, keys.Get(keys.Refresh)):
		return v.poll.refresh()
	case key.Matches(msg, keys.Get(keys.Mark)):
		v.marked.toggle(cur.d.Name)
		i = min(i+1, len(rows)-1) // on to the next, as file managers do
	case key.Matches(msg, keys.Get(keys.ClearMarks)):
		clear(v.marked)
	case key.Matches(msg, keys.As(keys.Select, "open")):
		return v.open(cur.d)
	}
	v.selected = rows[i].d.Name
	return nil
}

func parentRow(rows []*node, i int) int {
	for j := i - 1; j >= 0; j-- {
		if rows[j].depth < rows[i].depth {
			return j
		}
	}
	return -1
}

// open moves focus into a dataset's detail.
func (v *Datasets) open(d zfs.Dataset) tea.Cmd {
	v.selected = d.Name
	cmd := v.follow()
	v.focus = v.detail != nil
	return cmd
}

// follow makes the detail show the selected dataset, keeping the tab; in
// the split layout it runs on every selection change.
func (v *Datasets) follow() tea.Cmd {
	rows := v.rows()
	if len(rows) == 0 {
		return nil
	}
	if v.detail != nil && v.detail.ds.String() == rows[v.cursor(rows)].d.Name {
		return v.detail.init() // started already, unless adopted while drawing
	}
	if !v.adopt() {
		return nil
	}
	return v.detail.init()
}

// adopt points the detail at the selected dataset without reading
// anything, keeping the tab; it reports whether there is a detail.
func (v *Datasets) adopt() bool {
	rows := v.rows()
	if len(rows) == 0 {
		return false
	}
	d := rows[v.cursor(rows)].d
	if v.detail != nil && v.detail.ds.String() == d.Name {
		return true
	}
	ds, err := name.ParseDataset(d.Name)
	if err != nil {
		return false // names in a listing were validated when it was parsed
	}
	tab := dsTabSpace
	if v.detail != nil {
		tab = v.detail.tab
	}
	v.detail = newDSDetail(v.src, ds, d.Type, v.opts)
	v.detail.tab = tab
	return true
}

// listed returns the latest listing of the open dataset, or false once it
// is gone.
func (v *Datasets) listed(ds name.Dataset) (zfs.Dataset, bool) {
	for _, d := range v.poll.data {
		if d.Name == ds.String() {
			return d, true
		}
	}
	return zfs.Dataset{}, false
}

// Status implements ui.Screen.
func (v *Datasets) Status() ui.Status {
	st := ui.Status{Refreshed: v.poll.loaded, LastError: v.poll.lastErr}
	if !v.poll.loaded.IsZero() {
		pools := map[string]bool{}
		for _, d := range v.poll.data {
			pools[d.Pool.String()] = true
		}
		st.Figure = fmt.Sprintf("%d %s in %d %s", len(v.poll.data), plural(len(v.poll.data), "dataset", "datasets"),
			len(pools), plural(len(pools), "pool", "pools"))
	}
	return st
}

// mouse handles the wheel and clicks like Pools.mouse: rows in the tree,
// tabs and scrolling in the detail.
func (v *Datasets) mouse(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case ui.ScrollMsg:
		if v.focus {
			v.detail.scroll = max(v.detail.scroll+msg.Delta, 0)
			return nil, true
		}
		rows := v.rows()
		if len(rows) > 0 {
			v.selected = rows[max(min(v.cursor(rows)+msg.Delta, len(rows)-1), 0)].d.Name
		}
		if v.geo.split {
			return v.follow(), true
		}
		return nil, true
	case ui.ClickMsg:
		return v.click(msg), true
	}
	return nil, false
}

func (v *Datasets) click(msg ui.ClickMsg) tea.Cmd {
	inDetail := v.focus && !v.geo.split || v.geo.split && msg.X >= v.geo.left
	if inDetail && v.detail != nil {
		left, w := v.geo.detail()
		v.focus = true
		if t := v.detailFrame(true).TabAt(w, msg.X-left); msg.Y == 0 && t >= 0 {
			v.detail.tab, v.detail.scroll = t, 0
		}
		return v.detail.init()
	}
	v.focus = false
	rows := v.rows()
	row := v.first + msg.Y - listTop
	if msg.Y < listTop || row >= len(rows) {
		return nil
	}
	if rows[row].d.Name == v.selected {
		return v.open(rows[row].d)
	}
	v.selected = rows[row].d.Name
	if v.geo.split {
		return v.follow()
	}
	return nil
}

// changed reads the tree, and the open dataset's detail, now: a change
// ran.
func (v *Datasets) changed() tea.Cmd {
	cmd := v.poll.refresh()
	if v.detail != nil {
		cmd = tea.Batch(cmd, v.detail.props.refresh(), v.detail.snaps.refresh())
	}
	return cmd
}

// Selection implements ui.Selector: the dataset under the cursor.
func (v *Datasets) Selection() (ui.Target, bool) {
	rows := v.rows()
	if len(rows) == 0 {
		return ui.Target{}, false
	}
	d := rows[v.cursor(rows)].d
	kind := "filesystem"
	if d.Type == zfs.Volume {
		kind = "volume"
	}
	if len(v.marked) > 0 {
		return ui.Target{Backend: "zfs", Kind: kind, Names: v.marked.sorted()}, true
	}
	return ui.Target{Backend: "zfs", Kind: kind, Names: []string{d.Name}}, true
}

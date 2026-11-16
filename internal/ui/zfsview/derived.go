// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import "time"

// derived keeps a value computed from a view's data, such as the sorted
// snapshot list or the dataset tree, until the data or a setting it
// depends on changes. Views draw on every tick of the clock and cursor;
// recomputing these each time costs a copy and a sort of every snapshot.
type derived[K comparable, V any] struct {
	key K
	ok  bool
	v   V
}

// get returns the kept value for key, computing it with compute when the
// key differs from the last one. The value is shared: callers must not
// change it.
func (d *derived[K, V]) get(key K, compute func() V) V {
	if !d.ok || d.key != key {
		d.v, d.key, d.ok = compute(), key, true
	}
	return d.v
}

// listKey identifies a loaded list and how it is ordered.
type listKey struct {
	loaded  time.Time
	n       int
	sortBy  int
	reverse bool
}

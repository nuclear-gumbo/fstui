// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// Limits of the file walk: it stops at maxFiles files or after maxWalk,
// whichever comes first, and says so.
const (
	maxFiles = 200000
	maxWalk  = 60 * time.Second
	topDirs  = 5
)

// WalkResult is what the file walk found: for the files it read, their
// size against their allocation, which on ZFS is the size after
// compression.
type WalkResult struct {
	Files      int
	Bytes      uint64 // file sizes
	Allocated  uint64 // st_blocks × 512
	Poor       uint64 // bytes in files that compress less than incompressible
	Dirs       map[string]uint64
	Stopped    string // why the walk ended early, or ""
	Unreadable int    // directories the walk could not read
}

// Share is the part of the bytes read that does not compress.
func (r *WalkResult) Share() float64 { return float64(r.Poor) / float64(max(r.Bytes, 1)) }

// Walk measures compression per file under roots, each a mounted
// filesystem: it reads only metadata (lstat), never file contents, never
// follows a symbolic link, and stays on each root's filesystem, so child
// datasets mounted below are left to their own root. It stops at the
// limits or when ctx ends.
func Walk(ctx context.Context, roots []string) *WalkResult {
	wk := walker{ctx: ctx, deadline: time.Now().Add(maxWalk), r: &WalkResult{Dirs: map[string]uint64{}}}
	for _, root := range roots {
		st, ok := lstat(root)
		if !ok {
			wk.r.Unreadable++
			continue
		}
		wk.dev = st.Dev
		_ = filepath.WalkDir(root, wk.visit) // visit records every error it meets
		if wk.r.Stopped != "" {
			break
		}
	}
	return wk.r
}

type walker struct {
	ctx      context.Context //nolint:containedctx // one walk's cancellation
	deadline time.Time
	dev      uint64 // the current root's filesystem
	r        *WalkResult
}

// visit handles one entry: WalkDir reports an error only for a directory
// it cannot read, which is counted and skipped.
func (wk *walker) visit(p string, d fs.DirEntry, err error) error {
	r := wk.r
	switch {
	case wk.ctx.Err() != nil:
		r.Stopped = "cancelled"
		return fs.SkipAll
	case r.Files >= maxFiles:
		r.Stopped = fmt.Sprintf("after %d files", maxFiles)
		return fs.SkipAll
	case time.Now().After(wk.deadline):
		r.Stopped = fmt.Sprintf("after %s", maxWalk)
		return fs.SkipAll
	case err != nil:
		r.Unreadable++
		return fs.SkipDir
	}
	st, ok := lstat(p)
	switch {
	case !ok:
	case d.IsDir() && st.Dev != wk.dev:
		return fs.SkipDir // another filesystem: a child dataset or a mount
	case d.Type().IsRegular() && st.Size > 0:
		r.add(filepath.Dir(p), uint64(st.Size), uint64(st.Blocks)*512) //nolint:gosec // sizes are not negative here
	}
	return nil
}

func lstat(p string) (*syscall.Stat_t, bool) {
	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		return nil, false
	}
	return &st, true
}

func (r *WalkResult) add(dir string, size, alloc uint64) {
	r.Files++
	r.Bytes += size
	r.Allocated += alloc
	// A file whose allocation is close to its size did not compress.
	// Sparse files and files small enough to live in their dnode look
	// compressed; the result says so.
	if float64(size) < incompressible*float64(alloc) {
		r.Poor += size
		r.Dirs[dir] += size
	}
}

// lines are the walk's evidence lines.
func (r *WalkResult) lines(size func(uint64) string) []string {
	out := []string{fmt.Sprintf("files read: %d, %s in them, %s allocated; %s (%.0f%%) in files that do not compress",
		r.Files, size(r.Bytes), size(r.Allocated), size(r.Poor), 100*r.Share())}
	if r.Stopped != "" {
		out = append(out, "the walk stopped "+r.Stopped+"; the figures cover the files read")
	}
	if r.Unreadable > 0 {
		out = append(out, fmt.Sprintf("%d directories could not be read", r.Unreadable))
	}
	type dir struct {
		path  string
		bytes uint64
	}
	var dirs []dir
	for p, b := range r.Dirs {
		dirs = append(dirs, dir{p, b})
	}
	slices.SortFunc(dirs, func(a, b dir) int { return cmp.Or(cmp.Compare(b.bytes, a.bytes), cmp.Compare(a.path, b.path)) })
	for _, d := range dirs[:min(len(dirs), topDirs)] {
		out = append(out, "  "+size(d.bytes)+" that does not compress in "+d.path)
	}
	out = append(out, "sparse files and very small files look compressed; sizes settle once the transaction group is written")
	return out
}

// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/nuclear-gumbo/fstui/internal/format"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// PoolExport plans zpool export: every dataset is unmounted and the pool
// leaves the system until imported again (here or elsewhere).
func (b Builder) PoolExport(ctx context.Context, pool string) (*mutate.Plan, error) {
	pn, err := name.ParsePool(pool)
	if err != nil {
		return nil, fmt.Errorf("pool %s: %w", show(pool), err)
	}
	st, err := b.R.PoolTopology(ctx, pool)
	if err != nil {
		return nil, err
	}
	objs, err := b.R.PoolObjects(ctx, pn)
	if err != nil {
		return nil, err
	}
	p := newPlan(mutate.PoolExport, "Export "+show(pool), []string{pool}, map[string]string{"pool": pool},
		execute.Argv{Tool: execute.Zpool, Options: []string{"export"}, Operands: []string{pool}})
	blockIf(p, b.Unprivileged, "exporting a pool needs root")
	blockRoot(p, objs)
	mounted := 0
	for _, o := range objs {
		if o.Properties["mounted"].Text.Raw() == "yes" {
			mounted++
		}
	}
	affects(p,
		fmt.Sprintf("the %d mounted %s of %s are unmounted and its volumes go away; the export fails, changing nothing, if one is busy", mounted, format.Plural(mounted, "filesystem", "filesystems"), show(pool)),
		show(pool)+" leaves this system until it is imported again, here or on another machine")
	if st.Removing() || st.Expanding() || (st.Scan != nil && st.Scan.State == "SCANNING") {
		affects(p, "the running scrub, resilver, expansion or removal pauses, and goes on after the next import")
	}
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		st, err := b.R.PoolTopology(ctx, pool)
		if err != nil {
			return nil, err
		}
		return mutate.Fingerprint{pool + " (pool guid)": strconv.FormatUint(st.GUID, 10)}, nil
	}
	if p.Baseline, err = read(ctx); err != nil {
		return nil, err
	}
	p.Recheck = read
	return p, nil
}

// ImportSpec is a pool zpool import listed, and how to import it.
type ImportSpec struct {
	ID       string   // the numeric id zpool import printed
	NewName  string   // empty: keep its name
	ReadOnly bool     // -o readonly=on: nothing is written
	NoMount  bool     // -N: import without mounting
	Dirs     []string // -d: where to look; empty: zpool's default places
}

func (s ImportSpec) params() map[string]string {
	return map[string]string{
		"id": s.ID, "new": s.NewName, "readonly": boolParam(s.ReadOnly), "nomount": boolParam(s.NoMount), "dirs": joinList(s.Dirs),
	}
}

func importSpecFromParams(p map[string]string) ImportSpec {
	return ImportSpec{ID: p["id"], NewName: p["new"], ReadOnly: p["readonly"] == "true", NoMount: p["nomount"] == "true", Dirs: splitList(p["dirs"])}
}

// PoolImport plans zpool import of one listed pool, by its id. A pool
// last used by another system, or one zpool cannot import, is refused:
// fstui never imports with -f.
func (b Builder) PoolImport(ctx context.Context, s ImportSpec) (*mutate.Plan, error) {
	if _, err := strconv.ParseUint(s.ID, 10, 64); err != nil {
		return nil, fmt.Errorf("pool id %s is not a number", show(s.ID))
	}
	for _, d := range s.Dirs {
		// A control character (a newline, which separates the directories
		// in a saved plan) is refused as well as a relative path.
		if !strings.HasPrefix(d, "/") || strings.ContainsFunc(d, unicode.IsControl) {
			return nil, fmt.Errorf("search directory %s is not an absolute path", show(d))
		}
	}
	if s.NewName != "" {
		if _, err := name.ParsePool(s.NewName); err != nil {
			return nil, fmt.Errorf("new name %s: %w", show(s.NewName), err)
		}
	}
	found, err := b.R.Importable(ctx, s.Dirs)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(found, func(f zfs.Importable) bool { return f.ID == s.ID })
	if i < 0 {
		return nil, fmt.Errorf("zpool import no longer lists a pool with id %s", show(s.ID))
	}
	pool := found[i]
	as := pool.Name.Raw()
	if s.NewName != "" {
		as = s.NewName
	}
	p := newPlan(mutate.PoolImport, "Import "+show(pool.Name.Raw())+importAs(s.NewName), []string{as}, s.params(), s.argv())
	if err := b.importChecks(ctx, p, pool, as, s); err != nil {
		return nil, err
	}
	read := func(ctx context.Context) (mutate.Fingerprint, error) {
		found, err := b.R.Importable(ctx, s.Dirs)
		if err != nil {
			return nil, err
		}
		f := mutate.Fingerprint{"importable " + s.ID: mutate.Absent}
		if i := slices.IndexFunc(found, func(f zfs.Importable) bool { return f.ID == s.ID }); i >= 0 {
			f["importable "+s.ID] = found[i].State
		}
		return f, nil
	}
	if p.Baseline, err = read(ctx); err != nil {
		return nil, err
	}
	p.Recheck = read
	return p, nil
}

// argv is zpool import of the pool by its id, with s's options.
func (s ImportSpec) argv() execute.Argv {
	opts := []string{"import"}
	if s.NoMount {
		opts = append(opts, "-N")
	}
	if s.ReadOnly {
		opts = append(opts, "-o", "readonly=on")
	}
	for _, d := range s.Dirs {
		opts = append(opts, "-d", d)
	}
	operands := []string{s.ID}
	if s.NewName != "" {
		operands = append(operands, s.NewName)
	}
	return execute.Argv{Tool: execute.Zpool, Options: opts, Operands: operands}
}

func importAs(newName string) string {
	if newName == "" {
		return ""
	}
	return " as " + show(newName)
}

// importChecks blocks what zpool would refuse or what needs -f, and says
// what importing does.
func (b Builder) importChecks(ctx context.Context, p *mutate.Plan, pool zfs.Importable, as string, s ImportSpec) error {
	names, err := b.R.PoolNames(ctx)
	if err != nil {
		return err
	}
	blockIf(p, b.Unprivileged, "importing a pool needs root")
	blockIf(p, slices.Contains(names, as), "a pool named "+show(as)+" is imported: choose a new name for this one")
	text := strings.ToLower(joinTexts(pool.Status) + " " + joinTexts(pool.Action))
	switch {
	case strings.Contains(text, "another system"):
		blockIf(p, true, "zpool says "+show(pool.Name.Raw())+" was last used by another system: importing it while that system still uses it destroys it, and fstui does not force it (-f)")
	case pool.State != "ONLINE" && pool.State != "DEGRADED":
		blockIf(p, true, "zpool says "+show(pool.Name.Raw())+" cannot be imported ("+pool.State+"): "+joinTexts(pool.Action))
	case pool.State == "DEGRADED":
		affects(p, "devices are missing: the pool imports with less redundancy than it was built with")
	}
	switch {
	case s.ReadOnly:
		affects(p, "nothing is written to the pool: no changes are possible until it is exported and imported again")
	default:
		affects(p, show(as)+" is written to from now on: another system with the same disks must not import it at the same time")
	}
	if s.NoMount {
		affects(p, "the filesystems of "+show(as)+" stay unmounted until mounted one by one")
	} else {
		affects(p, "the filesystems of "+show(as)+" are mounted where their mountpoints say")
	}
	return nil
}

// joinTexts joins zpool's explanation lines into one sentence.
func joinTexts(ts []termtext.Text) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.Raw()
	}
	return strings.Join(parts, " ")
}

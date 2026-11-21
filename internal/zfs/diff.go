// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// DiffEntry is one line of zfs diff -H -F -t: a file or directory
// added, removed, modified or renamed since a snapshot.
type DiffEntry struct {
	Time    time.Time
	Change  byte          // '+', '-', 'M' or 'R'
	Type    byte          // 'F' file, '/' directory, '@' symlink, 'B', 'C', 'P', '=', '>' (zfs-diff(8))
	Path    termtext.Text // decoded; may hold any byte
	NewPath termtext.Text // for a rename
}

// MaxDiff bounds how many entries Diff keeps by default; Truncated says so.
const MaxDiff = 20000

// Diff reads what changed between snapshot from and to (a later
// snapshot, or the dataset itself for now). It needs root or the
// delegated diff permission.
func (r *Reader) Diff(ctx context.Context, from, to string) (entries []DiffEntry, truncated bool, err error) {
	ops := []string{from}
	if to != "" {
		ops = append(ops, to)
	}
	out, err := r.read(ctx, execute.Argv{Tool: execute.ZFS, Options: []string{"diff", "-H", "-F", "-t"}, Operands: ops})
	if err != nil {
		return nil, false, err
	}
	return parseDiff(out, cmp.Or(r.DiffMax, MaxDiff))
}

// ParseDiff parses zfs diff -H -F -t output, keeping MaxDiff entries.
func ParseDiff(data []byte) ([]DiffEntry, bool, error) { return parseDiff(data, MaxDiff) }

func parseDiff(data []byte, keep int) ([]DiffEntry, bool, error) {
	var out []DiffEntry
	for line := range bytes.Lines(data) {
		if len(out) == keep {
			return out, true, nil
		}
		f := strings.Split(strings.TrimRight(string(line), "\n"), "\t")
		if len(f) < 4 || len(f[1]) != 1 || len(f[2]) != 1 {
			return nil, false, fmt.Errorf("zfs diff: unexpected line %q", line)
		}
		sec, nsec, _ := strings.Cut(f[0], ".")
		s, err := strconv.ParseInt(sec, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("zfs diff: time %q: %w", f[0], err)
		}
		ns, _ := strconv.ParseInt(nsec, 10, 64)
		e := DiffEntry{Time: time.Unix(s, ns), Change: f[1][0], Type: f[2][0], Path: termtext.New(unescapeDiff(f[3]))}
		if len(f) > 4 {
			e.NewPath = termtext.New(unescapeDiff(f[4]))
		}
		out = append(out, e)
	}
	return out, false, nil
}

// unescapeDiff decodes zfs diff's escapes: a backslash, 0 and three
// octal digits stand for one byte (a space is \0040, ESC \0033).
func unescapeDiff(s string) string {
	if !strings.Contains(s, `\0`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+5 <= len(s) && s[i+1] == '0' {
			if n, err := strconv.ParseUint(s[i+2:i+5], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

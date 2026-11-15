// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// HistoryEntry is one record of zpool history -i.
type HistoryEntry struct {
	Time     time.Time // local time, as zpool prints it
	Internal bool      // logged by ZFS itself, not a command a user ran
	TXG      uint64    // transaction group of an internal event
	Text     termtext.Text
}

// historyTimeLayout is zpool history's timestamp, e.g. 2026-11-12.23:41:06.
const historyTimeLayout = "2006-01-02.15:04:05"

// ioctlRecord starts the internal record of an ioctl, as -i prints it:
// "(24ms) ioctl create", followed by indented lines with its arguments.
var ioctlRecord = regexp.MustCompile(`^\(\d+ms\) ioctl `)

// historyLine matches the start of an entry. Text after a newline in a
// logged command (property values may contain newlines) continues the
// previous entry instead.
var historyLine = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}\.\d{2}:\d{2}:\d{2}) (?:\[txg:(\d+)\] )?(.*)$`)

// ParseHistory parses the output of zpool history -i for one pool,
// oldest entry first, in the local time zone zpool printed.
func ParseHistory(data []byte) ([]HistoryEntry, error) {
	var out []HistoryEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, maxHistoryLine)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if !strings.HasPrefix(line, "History for '") {
				return nil, fmt.Errorf("zpool history: unexpected first line %s", termtext.New(line))
			}
			continue
		}
		m := historyLine.FindStringSubmatch(line)
		if m == nil {
			if len(out) == 0 {
				if line == "" {
					continue
				}
				return nil, fmt.Errorf("zpool history: text before the first entry: %s", termtext.New(line))
			}
			last := &out[len(out)-1]
			last.Text = termtext.New(last.Text.Raw() + "\n" + line)
			continue
		}
		e, err := historyEntry(m)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("zpool history: %w", err)
	}
	if first {
		return nil, fmt.Errorf("zpool history: empty output")
	}
	return out, nil
}

// maxHistoryLine bounds one line. A longer line fails the parse with an
// error rather than being cut silently.
const maxHistoryLine = 64 << 10

func historyEntry(m []string) (HistoryEntry, error) {
	t, err := time.ParseInLocation(historyTimeLayout, m[1], time.Local)
	if err != nil {
		return HistoryEntry{}, fmt.Errorf("zpool history: time %q: %w", m[1], err)
	}
	e := HistoryEntry{Time: t, Text: termtext.New(m[3]), Internal: ioctlRecord.MatchString(m[3])}
	if m[2] != "" {
		if e.TXG, err = strconv.ParseUint(m[2], 10, 64); err != nil {
			return HistoryEntry{}, fmt.Errorf("zpool history: txg %q: %w", m[2], err)
		}
		e.Internal = true
	}
	return e, nil
}

// PoolHistory reads zpool history -i for one pool. It needs root: on
// 2.3.9 unprivileged users get "permission denied".
func (r *Reader) PoolHistory(ctx context.Context, pool name.Pool) ([]HistoryEntry, error) {
	out, err := r.read(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"history", "-i"}, Operands: []string{pool.String()}})
	if err != nil {
		return nil, err
	}
	return ParseHistory(out)
}

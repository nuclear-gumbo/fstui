// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bufio"
	"bytes"
	"cmp"
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
	Seq      int // position in zpool's output, to merge the two kinds
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

// History is what zpool history -i printed for one pool: the most
// recent user commands and internal events, each oldest first, and how
// many of each there were.
type History struct {
	User, Internal           []HistoryEntry
	UserTotal, InternalTotal int
}

// keepHistory is how many entries of each kind History keeps. An old pool
// logs an internal event for every snapshot ever taken, millions over
// years; the commands people ran are kept apart so these never push them
// out.
const keepHistory = 10000

// defaultHistoryTimeout bounds zpool history, which grows with the
// pool's age, when limits.history_timeout is not given.
const defaultHistoryTimeout = 10 * time.Minute

// historyParser reads zpool history -i line by line, keeping the most
// recent keep entries of each kind.
type historyParser struct {
	h       History
	keep    int
	started bool
	last    *[]HistoryEntry // the kind the latest entry went to
	more    []string        // its continuation lines, joined once it ends
}

// line consumes one line of output.
func (p *historyParser) line(line string) error {
	if !p.started {
		if !strings.HasPrefix(line, "History for '") {
			return fmt.Errorf("zpool history: unexpected first line %s", termtext.New(line))
		}
		p.started = true
		return nil
	}
	m := historyLine.FindStringSubmatch(line)
	if m == nil {
		if p.last == nil {
			if line == "" {
				return nil
			}
			return fmt.Errorf("zpool history: text before the first entry: %s", termtext.New(line))
		}
		p.more = append(p.more, line)
		return nil
	}
	p.finish()
	e, err := historyEntry(m)
	if err != nil {
		return err
	}
	e.Seq = p.h.UserTotal + p.h.InternalTotal
	if e.Internal {
		p.last = &p.h.Internal
		p.h.InternalTotal++
	} else {
		p.last = &p.h.User
		p.h.UserTotal++
	}
	if len(*p.last) >= 2*p.keep { // drop the oldest half at once, not one by one
		*p.last = append([]HistoryEntry(nil), (*p.last)[len(*p.last)-p.keep:]...)
	}
	*p.last = append(*p.last, e)
	return nil
}

// finish joins the latest entry's continuation lines onto it.
func (p *historyParser) finish() {
	if p.last == nil || len(p.more) == 0 {
		return
	}
	e := &(*p.last)[len(*p.last)-1]
	e.Text = termtext.New(e.Text.Raw() + "\n" + strings.Join(p.more, "\n"))
	p.more = p.more[:0]
}

// result is the history read, trimmed to keep entries of each kind.
func (p *historyParser) result() (History, error) {
	if !p.started {
		return History{}, fmt.Errorf("zpool history: empty output")
	}
	p.finish()
	trim := func(es []HistoryEntry) []HistoryEntry { return es[max(len(es)-p.keep, 0):] }
	p.h.User, p.h.Internal = trim(p.h.User), trim(p.h.Internal)
	return p.h, nil
}

// ParseHistory parses the output of zpool history -i for one pool, in the
// local time zone zpool printed.
func ParseHistory(data []byte) (History, error) {
	p := historyParser{keep: keepHistory}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, maxHistoryLine)
	for sc.Scan() {
		if err := p.line(sc.Text()); err != nil {
			return History{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return History{}, fmt.Errorf("zpool history: %w", err)
	}
	return p.result()
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

// PoolHistory reads zpool history -i for one pool as it streams, keeping
// the most recent entries (keepHistory of each kind). It needs root: on
// 2.3.9 unprivileged users get "permission denied".
func (r *Reader) PoolHistory(ctx context.Context, pool name.Pool) (History, error) {
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(r.HistoryTimeout, defaultHistoryTimeout))
	defer cancel()
	p := historyParser{keep: cmp.Or(r.HistoryKeep, keepHistory)}
	var parseErr error
	err := r.run.Stream(ctx, execute.Argv{Tool: execute.Zpool, Options: []string{"history", "-i"}, Operands: []string{pool.String()}}, func(b []byte) {
		if parseErr == nil {
			parseErr = p.line(string(b))
		}
	})
	if err != nil {
		return History{}, fmt.Errorf("reading the history of %s: %w", pool, err)
	}
	if parseErr != nil {
		return History{}, parseErr
	}
	return p.result()
}

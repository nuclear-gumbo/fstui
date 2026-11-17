// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// IOStat is one row of zpool iostat over one interval: a pool, a vdev or
// a device. Rates are per second.
type IOStat struct {
	Name termtext.Text
	// Depth is 0 for the pool, then one more per level of the device tree
	// as zpool indents it.
	Depth int
	// Section is the label a row sits under, such as "special", "logs" or
	// "cache"; empty for the pool's main devices.
	Section               string
	ReadOps, WriteOps     uint64
	ReadBytes, WriteBytes uint64
	// ReadWait and WriteWait are total_wait: the average time an I/O
	// spent queued and on the device. Has* is false when there was no
	// I/O to measure (zpool prints "-").
	ReadWait, WriteWait       time.Duration
	HasReadWait, HasWriteWait bool
	// ReadDiskWait and WriteDiskWait are disk_wait: the time on the
	// device alone (0 without I/O).
	ReadDiskWait, WriteDiskWait time.Duration
}

// IOPool is a pool's row and the rows of its devices.
type IOPool struct {
	IOStat
	Devices []IOStat
}

// IOSample is every pool over one interval.
type IOSample []IOPool

// iostatValues is the number of value columns zpool iostat -l prints
// after the name: alloc, free, operations and bandwidth read and write,
// then total, disk, syncq and asyncq wait read and write, and scrub, trim
// and rebuild wait. Columns by position, as zpool-iostat(8) lists them.
const iostatValues = 17

const (
	colReadOps = iota + 2 // after alloc and free
	colWriteOps
	colReadBytes
	colWriteBytes
	colReadWait
	colWriteWait
	colReadDiskWait
	colWriteDiskWait
)

// IOStatArgv is the command the I/O view streams: every pool and its devices,
// exact numbers, latencies, one sample a second, without the first sample, which
// averages since the pool was imported.
var IOStatArgv = execute.Argv{
	Tool: execute.Zpool, Options: []string{"iostat", "-p", "-l", "-v", "-y"}, Operands: []string{"1"},
}

// IOStats streams zpool iostat until ctx is cancelled, calling sample
// with each complete interval.
func (r *Reader) IOStats(ctx context.Context, sample func(IOSample)) error {
	if err := r.streamIOStat(ctx, IOStatArgv, sample); err != nil {
		return fmt.Errorf("reading pool I/O: %w", err)
	}
	return nil
}

// streamIOStat streams a zpool iostat -p -l -v command until ctx ends,
// calling sample with each complete interval.
func (r *Reader) streamIOStat(ctx context.Context, a execute.Argv, sample func(IOSample)) error {
	p := IOStatParser{Sample: sample}
	var parseErr error
	err := r.run.Stream(ctx, a, func(line []byte) {
		if e := p.Line(string(line)); e != nil && parseErr == nil {
			parseErr = e
		}
	})
	if err != nil {
		return err //nolint:wrapcheck // the callers name what was read
	}
	return parseErr
}

// IOStatParser reads zpool iostat -p -l -v output line by line. Without
// -H the output keeps the device tree's indentation and repeats its
// header before each interval; pools are separated by lines of dashes.
// A sample is complete when the block of its last pool closes; the number
// of pools is learned from the first interval, which is therefore handed
// over when the second header arrives.
type IOStatParser struct {
	Sample func(IOSample)

	cur      IOSample
	inBlock  bool
	closed   int
	expected int // pools per interval, once known
	section  string
}

// Line consumes one line of output.
func (p *IOStatParser) Line(line string) error {
	trimmed := strings.TrimSpace(line)
	fields := strings.Fields(trimmed)
	switch {
	case len(fields) == 0:
		return nil
	case fields[0] == "capacity" || fields[0] == "pool":
		if fields[0] == "capacity" && len(p.cur) > 0 {
			p.expected = len(p.cur)
			p.emit()
		}
		return nil
	case strings.Trim(trimmed, "- ") == "":
		if p.inBlock {
			p.inBlock = false
			p.closed++
			if p.expected > 0 && p.closed >= p.expected {
				p.emit()
			}
		}
		return nil
	}
	head, values, ok := splitValues(line, iostatValues)
	if !ok {
		return fmt.Errorf("zpool iostat: %d values expected in %q", iostatValues, termtext.New(line).Display())
	}
	indent := len(head) - len(strings.TrimLeft(head, " "))
	name := strings.TrimSpace(head)
	switch {
	case !p.inBlock:
		p.inBlock, p.section = true, ""
		row, err := ioRow(name, 0, "", values)
		if err != nil {
			return err
		}
		p.cur = append(p.cur, IOPool{IOStat: row})
	case indent == 0:
		p.section = name // "special", "logs", "cache": a label with no figures
	default:
		row, err := ioRow(name, indent/2, p.section, values) //nolint:mnd // zpool indents by two
		if err != nil {
			return err
		}
		last := &p.cur[len(p.cur)-1]
		last.Devices = append(last.Devices, row)
	}
	return nil
}

func (p *IOStatParser) emit() {
	if len(p.cur) > 0 && p.Sample != nil {
		p.Sample(p.cur)
	}
	p.cur, p.closed, p.inBlock = nil, 0, false
}

// splitValues cuts the last n whitespace-separated fields off line and
// returns what precedes them unchanged, so a name with spaces survives.
func splitValues(line string, n int) (head string, values []string, ok bool) {
	rest := strings.TrimRight(line, " \t")
	values = make([]string, n)
	for i := n - 1; i >= 0; i-- {
		cut := strings.LastIndexAny(rest, " \t")
		if cut < 0 {
			return "", nil, false
		}
		values[i] = rest[cut+1:]
		rest = strings.TrimRight(rest[:cut], " \t")
	}
	return rest, values, strings.TrimSpace(rest) != ""
}

func ioRow(name string, depth int, section string, v []string) (IOStat, error) {
	row := IOStat{Name: termtext.New(name), Depth: depth, Section: section}
	for dst, i := range map[*uint64]int{
		&row.ReadOps: colReadOps, &row.WriteOps: colWriteOps, &row.ReadBytes: colReadBytes, &row.WriteBytes: colWriteBytes,
	} {
		if v[i] == "-" {
			continue
		}
		n, err := strconv.ParseUint(v[i], 10, 64)
		if err != nil {
			return IOStat{}, fmt.Errorf("zpool iostat: %s: %w", termtext.New(name).Display(), err)
		}
		*dst = n
	}
	var err error
	if row.ReadWait, row.HasReadWait, err = nanoseconds(v[colReadWait]); err != nil {
		return IOStat{}, err
	}
	if row.WriteWait, row.HasWriteWait, err = nanoseconds(v[colWriteWait]); err != nil {
		return IOStat{}, err
	}
	if row.ReadDiskWait, _, err = nanoseconds(v[colReadDiskWait]); err != nil {
		return IOStat{}, err
	}
	if row.WriteDiskWait, _, err = nanoseconds(v[colWriteDiskWait]); err != nil {
		return IOStat{}, err
	}
	return row, nil
}

// nanoseconds parses a -p latency; "-" means no I/O was measured.
func nanoseconds(s string) (time.Duration, bool, error) {
	if s == "-" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("zpool iostat latency %q: %w", s, err)
	}
	return time.Duration(n), true, nil
}

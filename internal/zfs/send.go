// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/zfs/name"
)

// SendOptions are the zfs send options that change a stream's size or
// what it carries (zfs-send(8)).
type SendOptions struct {
	Compressed    bool // -c: blocks as compressed on disk
	Raw           bool // -w: encrypted datasets stay encrypted; implies the on-disk size
	Intermediates bool // -I: every snapshot between two marked ones, not only the marked
}

// flags are the options as zfs send takes them, before -i or -I.
func (o SendOptions) flags() []string {
	var f []string
	if o.Compressed {
		f = append(f, "-c")
	}
	if o.Raw {
		f = append(f, "-w")
	}
	return f
}

// SendStep is one stream: a full stream of To when From is empty, else an
// incremental one from From (a snapshot or bookmark of the same
// dataset) to To.
type SendStep struct {
	From, To string
}

// Argv is the command that sends the step. With dryRun it only reports
// the estimated size (-n -P) and sends nothing.
func (s SendStep) Argv(o SendOptions, dryRun bool) execute.Argv {
	var opts []string
	opts = append(opts, "send")
	if dryRun {
		opts = append(opts, "-n", "-P")
	}
	opts = append(opts, o.flags()...)
	if s.From != "" {
		inc := "-i"
		if o.Intermediates {
			inc = "-I"
		}
		opts = append(opts, inc, s.From)
	}
	return execute.Argv{Tool: execute.ZFS, Options: opts, Operands: []string{s.To}}
}

// SendStream is one line of zfs send -n -P: a full stream, or an
// incremental one from From (as zfs prints it: -I gives the short name).
type SendStream struct {
	From  string
	To    string
	Bytes uint64
}

// SendEstimate is what zfs send -n -P estimates for one command.
type SendEstimate struct {
	Streams []SendStream
	Total   uint64
}

// ParseSendEstimate parses zfs send -n -P output: "full", "incremental"
// and "size" lines, tab-separated.
func ParseSendEstimate(data []byte) (SendEstimate, error) {
	var e SendEstimate
	sawSize := false
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		f := strings.Split(string(line), "\t")
		var err error
		switch {
		case len(f) == 3 && f[0] == "full":
			var s SendStream
			s.To = f[1]
			s.Bytes, err = strconv.ParseUint(f[2], 10, 64)
			e.Streams = append(e.Streams, s)
		case len(f) == 4 && f[0] == "incremental":
			var s SendStream
			s.From, s.To = f[1], f[2]
			s.Bytes, err = strconv.ParseUint(f[3], 10, 64)
			e.Streams = append(e.Streams, s)
		case len(f) == 2 && f[0] == "size":
			e.Total, err = strconv.ParseUint(f[1], 10, 64)
			sawSize = true
		default:
			return SendEstimate{}, fmt.Errorf("zfs send -n -P: unexpected line %q", line)
		}
		if err != nil {
			return SendEstimate{}, fmt.Errorf("zfs send -n -P: %q: %w", line, err)
		}
	}
	if !sawSize {
		return SendEstimate{}, fmt.Errorf("zfs send -n -P: no size line in %q", data)
	}
	return e, nil
}

// EstimateSend asks zfs for the size of one step's stream. It sends
// nothing. It needs root or the send permission (zfs allow).
func (r *Reader) EstimateSend(ctx context.Context, s SendStep, o SendOptions) (SendEstimate, error) {
	if _, err := name.ParseSnapshot(s.To); err != nil {
		return SendEstimate{}, err
	}
	if s.From != "" {
		_, serr := name.ParseSnapshot(s.From)
		_, berr := name.ParseBookmark(s.From)
		if serr != nil && berr != nil {
			return SendEstimate{}, fmt.Errorf("%q is not a snapshot or bookmark", s.From)
		}
	}
	out, err := r.read(ctx, s.Argv(o, true))
	if err != nil {
		return SendEstimate{}, err
	}
	return ParseSendEstimate(out)
}

// PlanSends turns marked snapshots into streams, one chain per dataset in
// name order: a full stream of the oldest marked snapshot, then
// incremental streams to each later one in creation order, or with
// Intermediates one stream from the oldest to the newest that carries
// every snapshot between them.
func PlanSends(marked []Dataset, o SendOptions) []SendStep {
	by := map[string][]Dataset{}
	var datasets []string
	for _, d := range marked {
		ds, _, ok := strings.Cut(d.Name, "@")
		if !ok {
			continue
		}
		if _, seen := by[ds]; !seen {
			datasets = append(datasets, ds)
		}
		by[ds] = append(by[ds], d)
	}
	slices.Sort(datasets)
	var steps []SendStep
	for _, ds := range datasets {
		snaps := by[ds]
		sortByTXG(snaps)
		steps = append(steps, SendStep{To: snaps[0].Name})
		switch {
		case len(snaps) == 1:
		case o.Intermediates:
			steps = append(steps, SendStep{From: snaps[0].Name, To: snaps[len(snaps)-1].Name})
		default:
			for i := 1; i < len(snaps); i++ {
				steps = append(steps, SendStep{From: snaps[i-1].Name, To: snaps[i].Name})
			}
		}
	}
	return steps
}

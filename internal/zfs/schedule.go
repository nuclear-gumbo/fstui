// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// ScrubSchedule is when a pool will next be scrubbed, and what will do it.
type ScrubSchedule struct {
	Next time.Time // zero when the schedule is known but not its next time
	By   string    // e.g. "systemd timer zfs-scrub-weekly@tank.timer"
}

// scrubTimer matches OpenZFS's timer units, zfs-scrub-weekly@<pool>.timer
// and zfs-scrub-monthly@<pool>.timer (contrib/systemd in 2.3.9).
var scrubTimer = regexp.MustCompile(`^zfs-scrub-(weekly|monthly)@(.+)\.timer$`)

// ParseScrubTimers reads systemctl list-timers --all --output=json and
// returns the pools that an OpenZFS scrub timer covers. Only enabled or
// started timers are listed. next is microseconds since 1970; 0 means
// systemd has not computed it.
func ParseScrubTimers(data []byte) (map[string]ScrubSchedule, error) {
	var timers []struct {
		Next uint64 `json:"next"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal(data, &timers); err != nil {
		return nil, fmt.Errorf("systemctl list-timers: %w", err)
	}
	out := map[string]ScrubSchedule{}
	for _, t := range timers {
		m := scrubTimer.FindStringSubmatch(t.Unit)
		switch {
		case m != nil:
			pool, err := unescapeUnit(m[2])
			if err != nil {
				return nil, fmt.Errorf("systemctl list-timers: unit %q: %w", t.Unit, err)
			}
			keepEarlier(out, pool, timerSchedule("systemd timer "+t.Unit, t.Next))
		case strings.HasPrefix(t.Unit, "zfs-scrub") && strings.HasSuffix(t.Unit, ".timer"):
			// Another scrub timer, such as NixOS's zfs-scrub.timer
			// (services.zfs.autoScrub): which pools it covers is not read,
			// so it is reported for every pool as possible.
			keepEarlier(out, AnyPool, timerSchedule("possibly systemd timer "+t.Unit, t.Next))
		}
	}
	return out, nil
}

// timerSchedule is a timer's schedule; next is microseconds since 1970,
// 0 when systemd has not computed it.
func timerSchedule(by string, next uint64) ScrubSchedule {
	s := ScrubSchedule{By: by}
	if next > 0 {
		s.Next = time.UnixMicro(int64(next)) //nolint:gosec // microseconds since 1970
	}
	return s
}

// keepEarlier records s for pool unless a schedule with an earlier known
// time is already there.
func keepEarlier(m map[string]ScrubSchedule, pool string, s ScrubSchedule) {
	if old, ok := m[pool]; !ok || !s.Next.IsZero() && (old.Next.IsZero() || s.Next.Before(old.Next)) {
		m[pool] = s
	}
}

// AnyPool keys a schedule that may cover every pool.
const AnyPool = "*"

// unescapeUnit undoes systemd's escaping of a unit instance: \xNN is the
// byte NN (systemd.unit(5)); a pool name has no "/" to restore.
func unescapeUnit(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+3 >= len(s) || s[i+1] != 'x' { // \xNN needs three more bytes
			return "", fmt.Errorf("bad escape at %d", i)
		}
		n, err := strconv.ParseUint(s[i+2:i+4], 16, 8)
		if err != nil {
			return "", fmt.Errorf("bad escape at %d: %w", i, err)
		}
		b.WriteByte(byte(n))
		i += 3
	}
	return b.String(), nil
}

// DebianScrubCron is the cron file Debian's zfsutils-linux installs; its
// script scrubs every ONLINE pool whose root dataset does not set
// DebianScrubProperty to "disable".
const (
	DebianScrubCron     = "/etc/cron.d/zfsutils-linux"
	DebianScrubProperty = "org.debian:periodic-scrub"
	debianScrubScript   = "/usr/lib/zfs-linux/scrub"
	// debianScrubLine is the schedule as shipped (Debian 13, 2.3.9):
	// 00:24 on days 8 to 14 when the day is a Sunday, the second Sunday.
	debianScrubLine = `24 0 8-14 * * root if [ $(date +\%w) -eq 0 ] && [ -x /usr/lib/zfs-linux/scrub ]; then /usr/lib/zfs-linux/scrub; fi`
)

// DebianCronSchedule reads the cron file: whether it runs the scrub
// script at all, and whether at the shipped time, which NextDebianScrub
// computes. A changed line is reported without a time rather than
// parsed: cron syntax is not worth guessing at.
func DebianCronSchedule(cron []byte) (runs, shipped bool) {
	for _, line := range strings.Split(string(cron), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, debianScrubScript) {
			continue
		}
		return true, line == debianScrubLine
	}
	return false, false
}

// NextDebianScrub is the first time after t that the shipped cron line
// runs: 00:24 on the second Sunday of a month, in t's location.
func NextDebianScrub(t time.Time) time.Time {
	y, m := t.Year(), t.Month()
	for range 13 {
		for day := 8; day <= 14; day++ {
			c := time.Date(y, m, day, 0, 24, 0, 0, t.Location())
			if c.Weekday() == time.Sunday && c.After(t) {
				return c
			}
		}
		m++
		if m > time.December {
			m, y = time.January, y+1
		}
	}
	return time.Time{}
}

// DebianScrubs reports whether Debian's script scrubs a pool, given the
// pool's health and its root dataset's DebianScrubProperty ("" or "-"
// when unset; "auto" and "enable" scrub; "disable" does not).
func DebianScrubs(health, property string) bool {
	if health != "ONLINE" {
		return false
	}
	switch property {
	case "", "-", "auto", "enable":
		return true
	}
	return false
}

// ScrubSchedules says when each pool will next be scrubbed: by an OpenZFS
// systemd timer (when systemctl was resolved), or by Debian's cron job
// (when r.CronFile exists), whichever comes first. Pools with neither are
// missing from the result.
func (r *Reader) ScrubSchedules(ctx context.Context, pools []Overview) (map[string]ScrubSchedule, error) {
	out := map[string]ScrubSchedule{}
	res, err := r.run.Read(ctx, execute.Argv{Tool: execute.Systemctl, Options: []string{"list-timers", "--all", "--output=json", "--no-pager"}})
	switch {
	case errors.Is(err, execute.ErrNotResolved): // no systemd here
	case err != nil:
		return nil, fmt.Errorf("reading scrub timers: %w", err)
	default:
		if out, err = ParseScrubTimers(res.Stdout); err != nil {
			return nil, err
		}
	}
	cron, err := r.debianCron(ctx, pools)
	if err != nil {
		return nil, err
	}
	for pool, s := range cron {
		keepEarlier(out, pool, s)
	}
	if s, ok := out[AnyPool]; ok {
		delete(out, AnyPool)
		for _, o := range pools {
			if _, has := out[o.Pool.Name.String()]; !has {
				out[o.Pool.Name.String()] = s
			}
		}
	}
	return out, nil
}

// debianCron returns the pools Debian's cron job scrubs.
func (r *Reader) debianCron(ctx context.Context, pools []Overview) (map[string]ScrubSchedule, error) {
	if r.CronFile == "" || len(pools) == 0 {
		return nil, nil
	}
	data, err := os.ReadFile(r.CronFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", r.CronFile, err)
	}
	runs, shipped := DebianCronSchedule(data)
	if !runs {
		return nil, nil
	}
	names := make([]string, len(pools))
	for i, o := range pools {
		names[i] = o.Pool.Name.String()
	}
	out, err := r.read(ctx, execute.Argv{Tool: execute.ZFS, Options: []string{"get", "-j", "-p", DebianScrubProperty}, Operands: names})
	if err != nil {
		return nil, err
	}
	roots, err := ParseDatasetGet(out)
	if err != nil {
		return nil, err
	}
	prop := map[string]string{}
	for _, d := range roots {
		prop[d.Name] = d.Properties[DebianScrubProperty].Text.Raw()
	}
	s := ScrubSchedule{By: "Debian's monthly cron job (" + r.CronFile + ")"}
	if shipped {
		s.Next = NextDebianScrub(time.Now())
	}
	result := map[string]ScrubSchedule{}
	for _, o := range pools {
		if DebianScrubs(o.Status.State, prop[o.Pool.Name.String()]) {
			result[o.Pool.Name.String()] = s
		}
	}
	return result, nil
}

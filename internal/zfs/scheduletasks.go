// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/nuclear-gumbo/fstui/internal/schedule"
)

// ScheduleTasks are the ZFS backend's periodic work for the Schedules
// tab: a scrub and a trim of every imported pool, through OpenZFS's own
// systemd timers or, without systemd, cron running zpool (the resolved
// path). Debian's scrub cron job is returned among the schedules fstui
// does not manage, so a pool is not scrubbed twice by mistake.
func (r *Reader) ScheduleTasks(ctx context.Context, zpool string) ([]schedule.Task, []schedule.Job, error) {
	pools, err := r.PoolNames(ctx)
	if err != nil {
		return nil, nil, err
	}
	var tasks []schedule.Task
	for _, p := range pools {
		for _, kind := range []string{"scrub", "trim"} {
			tasks = append(tasks, schedule.Task{
				Kind: kind, Target: p,
				Templates: map[string]string{
					schedule.Weekly:  "zfs-" + kind + "-weekly@.timer",
					schedule.Monthly: "zfs-" + kind + "-monthly@.timer",
				},
				Command: []string{zpool, kind, p},
			})
		}
	}
	var others []schedule.Job
	if r.CronFile != "" {
		data, err := os.ReadFile(r.CronFile)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, nil, fmt.Errorf("reading %s: %w", r.CronFile, err)
		default:
			if runs, _ := DebianCronSchedule(data); runs {
				others = append(others, schedule.Job{
					What: "scrub every pool", When: "2nd Sunday", By: "Debian's cron job " + r.CronFile,
					Note: "set " + DebianScrubProperty + "=disable on a pool's root dataset to leave it out",
				})
			}
		}
	}
	return tasks, others, nil
}

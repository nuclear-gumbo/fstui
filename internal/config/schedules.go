// SPDX-License-Identifier: GPL-3.0-or-later

package config

// Schedules are the settings of the Schedules tab: how often a schedule
// fstui writes runs fstui check, and when cron runs scrubs and trims.
// OpenZFS's systemd timers keep their own calendars (weekly, monthly).
type Schedules struct {
	CheckEvery string // one of CheckEvery
	// CronTime is "HH:MM", when cron starts scrubs and trims.
	CronTime string
	// ScrubWeekday and TrimWeekday are the weekday of a weekly scrub or
	// trim under cron; ScrubMonthday and TrimMonthday the day of a
	// monthly one.
	ScrubWeekday, TrimWeekday   string
	ScrubMonthday, TrimMonthday int
}

// CheckEvery are the intervals fstui check can run at from a schedule
// fstui writes; each is both a systemd timer interval and a cron line.
var CheckEvery = []string{"5m", "10m", "15m", "30m", "1h", "2h", "6h", "12h", "24h"}

// Weekdays are cron's weekday names, Sunday first.
var Weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func defaultSchedules() Schedules {
	return Schedules{CheckEvery: "15m", CronTime: "03:00", ScrubWeekday: "sun", TrimWeekday: "wed", ScrubMonthday: 1, TrimMonthday: 15}
}

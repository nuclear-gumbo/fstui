// SPDX-License-Identifier: GPL-3.0-or-later

package schedule

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// cronHeader starts the cron file fstui writes. fstui owns the file: it
// writes it whole, and keeps only the entries under its marker lines.
const cronHeader = `# Written by fstui: change it in the Schedules tab of the Config view.
# fstui rewrites this file whole and keeps only entries under a
# "# fstui:" line.
`

// cronMarker starts the line before each entry: "# fstui: <id> <when>".
const cronMarker = "# fstui: "

// cronEntry is one entry of fstui's cron file.
type cronEntry struct {
	when string // Weekly, Monthly or "every 15m"
	line string
}

// parseCron reads fstui's cron file: each entry is the line after a
// marker naming it. Lines without a marker are not fstui's.
func parseCron(data []byte) map[string]cronEntry {
	out := map[string]cronEntry{}
	lines := strings.Split(string(data), "\n")
	for i := 0; i+1 < len(lines); i++ {
		rest, ok := strings.CutPrefix(lines[i], cronMarker)
		if !ok {
			continue
		}
		id, when, _ := strings.Cut(rest, " ")
		out[id] = cronEntry{when: when, line: lines[i+1]}
		i++
	}
	return out
}

// cronFile writes the entries back, sorted by ID so the file is stable.
func cronFile(entries map[string]cronEntry) string {
	var b strings.Builder
	b.WriteString(cronHeader)
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "%s%s %s\n%s\n", cronMarker, id, entries[id].when, entries[id].line)
	}
	return b.String()
}

// cronCommand quotes argv for the shell cron runs it with, and escapes
// %, which cron turns into a newline.
func cronCommand(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@=-") == "" {
			q[i] = a
		} else {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.ReplaceAll(strings.Join(q, " "), "%", `\%`)
}

// taskCron is the cron line of a task run weekly or monthly.
func taskCron(t Task, period string, s config.Schedules) string {
	hh, mm, _ := strings.Cut(s.CronTime, ":")
	h, _ := strconv.Atoi(hh)
	m, _ := strconv.Atoi(mm)
	weekday, monthday := s.ScrubWeekday, s.ScrubMonthday
	if t.Kind == "trim" {
		weekday, monthday = s.TrimWeekday, s.TrimMonthday
	}
	dom, dow := "*", "*"
	if period == Weekly {
		dow = strconv.Itoa(slices.Index(config.Weekdays, weekday))
	} else {
		dom = strconv.Itoa(monthday)
	}
	return fmt.Sprintf("%d %d %s * %s root %s", m, h, dom, dow, cronCommand(t.Command))
}

// checkCron is the cron line running fstui check every interval. Its
// output goes nowhere unless it fails: cron mails what a job prints.
func checkCron(binary, every string) string {
	when := map[string]string{
		"5m": "*/5 * * * *", "10m": "*/10 * * * *", "15m": "*/15 * * * *", "30m": "*/30 * * * *",
		"1h": "0 * * * *", "2h": "0 */2 * * *", "6h": "0 */6 * * *", "12h": "0 */12 * * *", "24h": "0 0 * * *",
	}[every]
	return when + " root " + cronCommand([]string{binary, "check"}) + " >/dev/null"
}

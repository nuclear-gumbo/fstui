// SPDX-License-Identifier: GPL-3.0-or-later

// Package format writes sizes and durations the way fstui shows them, in
// the views, in fstui check and in notifications.
package format

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var iecUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// oneDecimalBelow: sizes under 100 of their unit get one decimal place,
// larger ones none.
const oneDecimalBelow = 100

// Size formats a byte count in IEC units, or exactly with digits grouped
// in threes when exact is set.
func Size(n uint64, exact bool) string {
	if exact {
		return grouped(n)
	}
	v, unit := float64(n), 0
	// 1023.7 MiB would print as "1024 MiB"; carry into the next unit.
	for v >= 1024-0.5 && unit < len(iecUnits)-1 {
		v /= 1024
		unit++
	}
	switch {
	case unit == 0:
		return fmt.Sprintf("%d B", n)
	case v < oneDecimalBelow && fmt.Sprintf("%.1f", v) != "100.0":
		return fmt.Sprintf("%.1f %s", v, iecUnits[unit])
	}
	return fmt.Sprintf("%.0f %s", v, iecUnits[unit])
}

func grouped(n uint64) string {
	s := strconv.FormatUint(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// Age is a duration in its largest whole unit: 45s, 14m, 2h, 5d.
func Age(d time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d/time.Second)))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < day:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/day))
}

// Plural is one when n is 1, else many: "1 pool", "3 pools".
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

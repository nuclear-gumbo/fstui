// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package actions

import (
	"context"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/schedule"
	"github.com/nuclear-gumbo/fstui/internal/ui"
)

// ScheduleOffers returns the changes offered for a schedule in the
// Config view's Schedules tab: on and off for fstui check and watch,
// weekly, monthly and off for a backend's task. Each is checked by
// building its plan, so one that cannot be made, or changes nothing,
// shows why before it is chosen.
func ScheduleOffers(b schedule.Builder) func(ui.Target) (string, []Offer) {
	return func(t ui.Target) (string, []Offer) {
		if t.Backend != "system" || t.Kind != "schedule" || len(t.Names) != 1 {
			return "", nil
		}
		id := t.Names[0]
		choices := []struct{ key, when, name string }{{"w", schedule.Weekly, "Weekly"}, {"m", schedule.Monthly, "Monthly"}, {"o", schedule.Off, "Off"}}
		what := strings.Replace(id, ":", " ", 1)
		if id == schedule.CheckID || id == schedule.WatchID {
			choices = []struct{ key, when, name string }{{"n", schedule.On, "On"}, {"o", schedule.Off, "Off"}}
			what = "fstui " + id
		}
		offers := make([]Offer, 0, len(choices))
		for _, c := range choices {
			build := func(ctx context.Context, _ map[string]string) (*mutate.Plan, error) { return b.Set(ctx, id, c.when) }
			offers = append(offers, Offer{
				Key: c.key, Op: mutate.ScheduleSet, Name: c.name, Build: build,
				Check: func(ctx context.Context) string {
					p, err := build(ctx, nil)
					if err != nil {
						return err.Error()
					}
					return strings.Join(p.Blocked, "; ")
				},
			})
		}
		return what, offers
	}
}

// Either offers what the first gives for a target, else what the second
// does: the backend's changes beside the schedules'.
func Either(a, b func(ui.Target) (string, []Offer)) func(ui.Target) (string, []Offer) {
	return func(t ui.Target) (string, []Offer) {
		if title, offers := a(t); offers != nil {
			return title, offers
		}
		return b(t)
	}
}

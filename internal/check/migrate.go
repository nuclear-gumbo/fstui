// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// before08 maps the kinds of problem fstui 0.8.0 kept in its state
// ("health:tank") to the templates that find them now.
var before08 = map[string]string{
	"health": "pool-unhealthy", "data": "data-errors", "capacity": "capacity", "fragmentation": "fragmentation",
	"scrub": "scrub-overdue", "checkpoint": "checkpoint-held", "version": "version-mismatch",
}

// migrate renames the problems a 0.8.0 state file holds to the keys the
// active alerts give them, with their states as the alerts set them, so
// the first run after an upgrade neither sends an open problem again nor
// says it cleared. One no alert would find any more is dropped without a
// message.
func migrate(s State, c config.Config) State {
	for key, e := range s.Problems {
		kind, subject, _ := strings.Cut(key, ":")
		name, old := before08[kind]
		if !old {
			continue
		}
		delete(s.Problems, key)
		t, al, ok := activeTemplate(c, name)
		if !ok {
			continue
		}
		t = al.Effective(t)
		if subject == "" {
			subject = "system"
		}
		switch {
		case kind == "health": // the pool's state, as before
		case e.State == "FULL":
			e.State = "error"
		default:
			e.State = t.Severity
		}
		s.Problems["alert:"+t.Name+"@"+t.Value+":"+subject] = e
	}
	return s
}

// activeTemplate is the first active alert on template name, and the
// template.
func activeTemplate(c config.Config, name string) (config.Template, config.Alert, bool) {
	for _, al := range c.ActiveAlerts() {
		if al.Template != name {
			continue
		}
		if t, ok := c.Notifications.TemplateNamed(name); ok {
			return t, al, true
		}
	}
	return config.Template{}, config.Alert{}, false
}

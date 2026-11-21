// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/config"
)

// The choices of "How often", as TrueNAS offers Immediately, Hourly,
// Daily, Never: a word, not a duration to type.
const asSettings = "default"

var repeatChoices = []string{asSettings, "0", "1h", "6h", "24h", "168h"}

func repeatShow(v string) string {
	switch v {
	case asSettings:
		return "as the Notifications settings say"
	case "0":
		return "once"
	case "1h":
		return "every hour"
	case "6h":
		return "every 6 hours"
	case "24h":
		return "every day"
	case "168h":
		return "every week"
	}
	return "every " + v
}

// asTemplate is the choice that leaves a setting as the kind of alert
// sets it.
const asTemplate = "default"

// open starts the form for a row: your own alert's form when the row is
// yours, else the alert's.
func (a *Alerts) open(r alertRow) {
	if !a.canEdit() {
		return
	}
	if a.book.pools != nil {
		a.book.poolList = a.book.pools()
	}
	a.message = ""
	if r.group == yours {
		a.openOwn(r)
		return
	}
	var al config.Alert
	if r.alert >= 0 {
		al = a.book.alerts()[r.alert]
	}
	t := al.Effective(r.t)
	v := map[string]string{
		"pools": strings.Join(al.Pools, ", "), "channels": strings.Join(al.Channels, ", "),
		"value": t.Value, "error_value": t.ErrorValue, "severity": t.Severity,
		"text": t.Text, "resolved_text": t.ResolvedText,
	}
	alertTimes(v, al)
	title := r.t.Desc
	if r.alert < 0 {
		title += " (off; finishing the form turns it on)"
	}
	a.form = &entryForm{alert: r.alert, template: r.t.Name, fieldForm: fieldForm{title: title, values: v, fieldsOf: func(v map[string]string) []formField {
		return a.alertFields(r.t, v, false)
	}}}
}

// openOwn starts the form for your own alert: what it watches, then the
// same settings as any other.
func (a *Alerts) openOwn(r alertRow) {
	if !a.canEdit() {
		return
	}
	if a.book.pools != nil {
		a.book.poolList = a.book.pools()
	}
	t := r.t
	if t.Name == "" {
		t = config.Template{Metric: "capacity", Op: ">=", Value: "70", Severity: "warning", Text: "{pool} is {value} full"}
	}
	var al config.Alert
	if r.alert >= 0 {
		al = a.book.alerts()[r.alert]
	}
	v := map[string]string{
		"description": t.Desc, "name": t.Name, "metric": t.Metric, "op": t.Op,
		"pools": strings.Join(al.Pools, ", "), "channels": strings.Join(al.Channels, ", "),
		"value": t.Value, "error_value": t.ErrorValue, "severity": t.Severity,
		"text": t.Text, "resolved_text": t.ResolvedText,
	}
	alertTimes(v, al)
	title := "Your own alert"
	if t.Name != "" {
		title = t.Desc
	}
	a.form = &entryForm{alert: r.alert, template: t.Name, own: true, fieldForm: fieldForm{title: title, values: v, fieldsOf: func(v map[string]string) []formField {
		t := config.Template{Metric: v["metric"], Op: v["op"], Value: v["value"], Severity: v["severity"], Text: v["text"]}
		return append(ownFields(v), a.alertFields(t, v, true)...)
	}}}
}

// alertTimes fills how often and whether the clearing is sent.
func alertTimes(v map[string]string, al config.Alert) {
	v["repeat"], v["resolved"] = asSettings, asTemplate
	if al.Repeat != nil {
		v["repeat"] = config.FormatDuration(*al.Repeat)
	}
	if al.Resolved != nil {
		v["resolved"] = strconv.FormatBool(*al.Resolved)
	}
}

// ownFields are what your own alert watches.
func ownFields(v map[string]string) []formField {
	m, _ := config.MetricNamed(v["metric"])
	metrics := make([]string, len(config.Metrics))
	for i, x := range config.Metrics {
		metrics[i] = x.Name
	}
	label := func(name string) string {
		x, _ := config.MetricNamed(name)
		return x.Label
	}
	const g = "What it watches"
	return []formField{
		{key: "description", label: "Description", group: g, hint: "What it is, in a few words, as the list shows it: \"Backup pool is nearly full\""},
		{key: "name", label: "Short name", group: g, hint: "Lower-case letters, digits, - and _, for the config file: backup-full"},
		{key: "metric", label: "Measure", group: g, choices: metrics, show: label, hint: "Watches " + m.Desc},
		{key: "op", label: "Sent when it is", group: g, choices: config.Ops, show: config.OpWords},
	}
}

// alertFields are where and when an alert is sent, and what it says. A
// threshold is asked for when the kind has one, or always for your own.
func (a *Alerts) alertFields(t config.Template, v map[string]string, own bool) []formField {
	m, _ := config.MetricNamed(t.Metric)
	pools := slices.Clone(a.book.poolList)
	var chs []string
	for _, ch := range a.book.cfg.Notifications.Channels {
		chs = append(chs, ch.Name)
	}
	for _, k := range []string{"pools", "channels"} { // keep names the file lists that are not here now
		for _, name := range splitNames(v[k]) {
			if list := map[string]*[]string{"pools": &pools, "channels": &chs}[k]; !slices.Contains(*list, name) {
				*list = append(*list, name)
			}
		}
	}
	out := []formField{
		{key: "pools", label: "Pools", group: "Where", multi: true, choices: pools, placeholder: "every pool", hint: "Tick the pools to watch; none ticked watches every pool"},
		{key: "channels", label: "Channels", group: "Where", multi: true, choices: chs, placeholder: "every channel", hint: "Tick where to send; none ticked sends to every channel"},
	}
	if own || t.When() != "" || m.Kind == "events" {
		out = append(out,
			formField{key: "value", label: "Threshold", group: "When", hint: unitHint(m)},
			formField{key: "error_value", label: "Error from", group: "When", placeholder: "none", hint: "Optional: past this it is sent as an error. " + unitHint(m)})
	}
	return append(
		out,
		formField{key: "severity", label: "Severity", group: "When", choices: config.ProblemSeverities, hint: "A channel hears what is at or above its least severity"},
		formField{key: "repeat", label: "How often", group: "When", choices: withChoice(repeatChoices, v["repeat"]), show: repeatShow, hint: "How often it is sent again while it lasts; the Notifications settings say every " + config.FormatDuration(a.book.cfg.Notifications.Repeat)},
		formField{key: "resolved", label: "When it clears", group: "When", choices: []string{asTemplate, "true", "false"}, show: resolvedShow},
		formField{key: "text", label: "Message", group: "Message", hint: placeholderHint},
		formField{key: "resolved_text", label: "Cleared message", group: "Message", placeholder: "a line naming the problem", hint: placeholderHint},
	)
}

func resolvedShow(v string) string {
	switch v {
	case "true":
		return "say so"
	case "false":
		return "say nothing"
	}
	return "as the Notifications settings say"
}

// placeholderHint names what a message can include.
var placeholderHint = func() string {
	names := make([]string, 0, len(config.Placeholders))
	for k := range config.Placeholders {
		names = append(names, k)
	}
	slices.Sort(names)
	return "A message can include " + strings.Join(names, " ")
}()

func unitHint(m config.Metric) string {
	switch m.Kind {
	case "percent":
		return "A percentage, such as 80"
	case "bytes":
		return "A size, such as 500G or 2T"
	case "days":
		return "A number of days"
	case "duration":
		return "A duration, such as 24h"
	case "ms":
		return "Milliseconds"
	case "state":
		return "A state, such as ONLINE or DEGRADED"
	case "events":
		return "info, notice, warn or error"
	}
	return "A number"
}

// finish applies the form to the unsaved alerts, or keeps it open with
// what is wrong.
func (a *Alerts) finish(keep bool) {
	f := a.form
	if !keep {
		a.form = nil
		return
	}
	err := a.apply(f)
	if err != nil {
		f.problem = err.Error()
		return
	}
	a.form = nil
	a.review = a.stage()
}

func (a *Alerts) apply(f *entryForm) error {
	al, err := alertOf(f.values)
	if err != nil {
		return err
	}
	if f.own {
		return a.applyOwn(f, al)
	}
	t, _ := a.book.lookup(f.template)
	al.Template = t.Name
	overrides(&al, t, f.values)
	if err := config.CheckTemplate(al.Effective(t)); err != nil {
		return err //nolint:wrapcheck // the message names the field
	}
	a.put(f, al)
	return nil
}

// applyOwn checks your own alert's condition, then writes it and what
// turns it on. Nothing changes when it is wrong.
func (a *Alerts) applyOwn(f *entryForm, al config.Alert) error {
	v := f.values
	t := config.Template{
		Name: strings.TrimSpace(v["name"]), Desc: strings.TrimSpace(v["description"]), Metric: v["metric"], Op: v["op"],
		Value: strings.TrimSpace(v["value"]), ErrorValue: strings.TrimSpace(v["error_value"]), Severity: v["severity"],
		Text: strings.TrimSpace(v["text"]), ResolvedText: strings.TrimSpace(v["resolved_text"]),
	}
	if t.Desc == "" {
		return errors.New("description: say what it is, as the list will show it")
	}
	if err := config.CheckNewTemplate(t); err != nil {
		return err //nolint:wrapcheck // the message names the field
	}
	taken := func(x config.Template) bool { return x.Name == t.Name }
	if t.Name != f.template && (slices.ContainsFunc(a.book.templates(), taken) || slices.ContainsFunc(config.BuiltinTemplates, taken)) {
		return fmt.Errorf("short name: %s is taken", t.Name)
	}
	e := a.book.startEdit()
	if i := slices.IndexFunc(e.templates, func(x config.Template) bool { return x.Name == f.template }); f.template != "" && i >= 0 {
		e.templates[i] = t
	} else {
		e.templates = append(e.templates, t)
	}
	for i := range e.alerts { // renamed: what turns it on follows
		if f.template != "" && e.alerts[i].Template == f.template {
			e.alerts[i].Template = t.Name
		}
	}
	al.Template = t.Name
	a.put(f, al)
	return nil
}

// put stores the alert the form edited, or adds it.
func (a *Alerts) put(f *entryForm, al config.Alert) {
	e := a.book.startEdit()
	if f.alert >= 0 && f.alert < len(e.alerts) {
		e.alerts[f.alert] = al
	} else {
		e.alerts = append(e.alerts, al)
	}
	a.follow(al.Template)
}

// alertOf reads where and how often from the form.
func alertOf(v map[string]string) (config.Alert, error) {
	al := config.Alert{Pools: splitNames(v["pools"]), Channels: splitNames(v["channels"])}
	if r := v["repeat"]; r != asSettings {
		d, err := time.ParseDuration(r)
		if r == "0" {
			d, err = 0, nil
		}
		if err != nil {
			return al, fmt.Errorf("how often: %w", err)
		}
		al.Repeat = &d
	}
	if r := v["resolved"]; r != asTemplate {
		b := r == "true"
		al.Resolved = &b
	}
	return al, nil
}

// overrides keeps in the alert only what differs from the kind of alert,
// so a later change to fstui's defaults still reaches it.
func overrides(al *config.Alert, t config.Template, v map[string]string) {
	for _, f := range []struct {
		dst      *string
		key, def string
	}{
		{&al.Value, "value", t.Value},
		{&al.ErrorValue, "error_value", t.ErrorValue},
		{&al.Severity, "severity", t.Severity},
		{&al.Text, "text", t.Text},
		{&al.ResolvedText, "resolved_text", t.ResolvedText},
	} {
		if s, ok := v[f.key]; ok && strings.TrimSpace(s) != f.def {
			*f.dst = strings.TrimSpace(s)
		}
	}
}

// splitNames reads names separated by commas or spaces.
func splitNames(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

// withChoice adds a value the file holds to the choices when it is not
// one of them, so opening the form never changes it.
func withChoice(choices []string, v string) []string {
	if v == "" || slices.Contains(choices, v) {
		return choices
	}
	return append(slices.Clone(choices), v)
}

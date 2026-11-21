// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const schemaFile = "../../docs/config.schema.json"

// The JSON schema of config.yaml in the docs is the registry's, with the
// lists and maps described by hand: editors use it to complete and check
// the file. make docs rewrites it.
func TestSchemaIsCurrent(t *testing.T) {
	want, err := json.MarshalIndent(schema(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	got, err := os.ReadFile(schemaFile)
	if err == nil && bytes.Equal(got, want) {
		return
	}
	if os.Getenv("FSTUI_UPDATE_DOCS") != "" {
		if err := os.WriteFile(schemaFile, want, 0o644); err != nil { //nolint:gosec // a documentation file
			t.Fatal(err)
		}
		return
	}
	t.Errorf("%s is out of date with the settings registry; run make docs", schemaFile)
}

type obj = map[string]any

// schema is config.yaml as a JSON schema (draft 2020-12).
func schema() obj {
	root := obj{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "fstui config.yaml",
		"description": "Every key is optional. Files in config.d/ take the same keys. " +
			"See docs/configuration.md.",
		"type": "object", "additionalProperties": false, "properties": obj{},
	}
	d := Default()
	for _, s := range Settings(nil) {
		parent := root
		parts := strings.Split(s.Path, ".")
		for _, p := range parts[:len(parts)-1] {
			props := parent["properties"].(obj) //nolint:forcetypeassert // built here
			if props[p] == nil {
				props[p] = obj{"type": "object", "additionalProperties": false, "properties": obj{}}
			}
			parent = props[p].(obj) //nolint:forcetypeassert // built here
		}
		parent["properties"].(obj)[parts[len(parts)-1]] = specSchema(s, s.get(&d)) //nolint:forcetypeassert // built here
	}
	props := root["properties"].(obj) //nolint:forcetypeassert // built here
	section := func(name string) obj {
		if props[name] == nil {
			props[name] = obj{"type": "object", "additionalProperties": false, "properties": obj{}}
		}
		return props[name].(obj)["properties"].(obj) //nolint:forcetypeassert // built here
	}
	keyList := obj{"oneOf": []any{obj{"type": "string"}, obj{"type": "array", "items": obj{"type": "string"}}}}
	props["keys"] = obj{
		"description": "Rebind actions: action name to one key or a list of keys (docs/keys.md).",
		"type":        "object", "additionalProperties": keyList,
	}
	colour := obj{"type": "string", "pattern": "^#[0-9A-Fa-f]{6}$"}
	colourProps := obj{}
	for _, tok := range []string{"accent", "ok", "warn", "error", "muted", "border", "key"} {
		colourProps[tok] = obj{"oneOf": []any{colour, obj{
			"type": "object", "additionalProperties": false,
			"properties": obj{"dark": colour, "light": colour},
		}}}
	}
	props["colors"] = obj{
		"description": "Override single colours of the theme: one colour, or one for dark and one for light terminals.",
		"type":        "object", "additionalProperties": false, "properties": colourProps,
	}
	severity := obj{"enum": SeverityNames}
	section("events")["filters"] = obj{
		"description": "Saved filters, cycled with f in the Events view. A list in a later file replaces this one.",
		"type":        "array",
		"items": obj{
			"type": "object", "additionalProperties": false, "required": []string{"name"},
			"properties": obj{
				"name":     obj{"type": "string"},
				"severity": obj{"oneOf": []any{severity, obj{"type": "array", "items": severity}}},
				"pool":     obj{"type": "string"},
				"class":    obj{"type": "string"},
				"text":     obj{"type": "string"},
			},
		},
	}
	section("dashboard")["panels"] = obj{
		"description": "Dashboard panels, in order. A list in a later file replaces this one.",
		"type":        "array", "items": obj{"enum": DashboardPanels},
	}
	str := obj{"type": "string"}
	section("notifications")["channels"] = obj{
		"description": "Where fstui check and watch send. Secrets go in secrets.yaml. A list in a later file replaces this one.",
		"type":        "array",
		"items": obj{
			"type": "object", "additionalProperties": false, "required": []string{"name", "type"},
			"properties": obj{
				"name":          obj{"type": "string", "pattern": channelName.String()},
				"type":          obj{"enum": ChannelTypes},
				"url":           str,
				"topic":         str,
				"chat_id":       str,
				"room":          str,
				"user_key":      str,
				"host":          str,
				"port":          obj{"type": "integer", "minimum": 1, "maximum": 65535},
				"from":          str,
				"to":            obj{"oneOf": []any{str, obj{"type": "array", "items": str}}},
				"user":          str,
				"ca_file":       str,
				"secret_file":   str,
				"insecure_http": obj{"type": "boolean"},
				"min_severity":  obj{"enum": ProblemSeverities},
			},
		},
	}
	metrics := make([]string, len(Metrics))
	for i, m := range Metrics {
		metrics[i] = m.Name
	}
	text := obj{"type": "string", "maxLength": textMax}
	section("notifications")["templates"] = obj{
		"description": "Your own alert templates, in the form of the built-in ones; one named like a built-in template replaces it. A list in a later file replaces this one.",
		"type":        "array", "maxItems": templatesMax,
		"items": obj{
			"type": "object", "additionalProperties": false, "required": []string{"name", "metric", "value", "text"},
			"properties": obj{
				"name":          obj{"type": "string", "pattern": templateName.String()},
				"description":   str,
				"metric":        obj{"enum": metrics},
				"op":            obj{"enum": Ops},
				"value":         str,
				"error_value":   str,
				"severity":      obj{"enum": ProblemSeverities},
				"text":          text,
				"resolved_text": text,
			},
		},
	}
	names := obj{"oneOf": []any{str, obj{"type": "array", "items": str}}}
	section("notifications")["alerts"] = obj{
		"description": "What is sent: each alert turns a template on for some pools and channels, or all. None listed: the defaults, from the thresholds. A list in a later file replaces this one.",
		"type":        "array", "maxItems": alertsMax,
		"items": obj{
			"type": "object", "additionalProperties": false, "required": []string{"template"},
			"properties": obj{
				"template":      str,
				"pools":         names,
				"channels":      names,
				"value":         str,
				"error_value":   str,
				"severity":      obj{"enum": ProblemSeverities},
				"text":          text,
				"resolved_text": text,
				"repeat":        str,
				"resolved":      obj{"type": "boolean"},
			},
		},
	}
	return root
}

// specSchema describes one registry setting, with its default.
func specSchema(s Spec, def string) obj {
	o := obj{"description": s.Desc + "."}
	switch s.Kind {
	case KindInt, KindSeconds:
		o["type"], o["minimum"], o["maximum"] = "integer", s.Min, s.Max
		o["default"], _ = strconv.Atoi(def)
	case KindDuration:
		o["type"], o["pattern"] = "string", `^(0|([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+)$`
		o["description"] = s.Desc + ", " + fmtDuration(s.MinDur) + " to " + fmtDuration(s.MaxDur) + "."
		o["default"] = def
	case KindFloat:
		o["type"], o["minimum"], o["maximum"] = "number", s.MinFloat, s.MaxFloat
		o["default"], _ = strconv.ParseFloat(def, 64)
	case KindBool:
		o["type"] = "boolean"
		o["default"] = def == "true"
	case KindString:
		o["type"], o["pattern"], o["default"] = "string", s.Pattern.String(), def
	case KindChoice:
		if s.Choices == nil { // a theme: shipped or a file's name
			o["type"], o["default"] = "string", def
			break
		}
		var enum []any
		for _, c := range s.Choices {
			enum = append(enum, c)
			if c == "true" || c == "false" { // YAML reads these as booleans
				enum = append(enum, c == "true")
			}
		}
		o["enum"], o["default"] = enum, def
	}
	return o
}

// The schema accepts what fstui accepts and refuses what it refuses, on
// the examples of docs/configuration.md and on mistakes a person makes.
func TestSchemaAgreesWithParser(t *testing.T) {
	good := `theme: default
mouse: true
ascii: auto
thresholds: {capacity_warn: 80, capacity_error: 90, checkpoint_age: 24h}
refresh: {pools: 5}
ui: {start_view: dashboard, cursor_blink: false}
events:
  filters:
    - {name: errors only, severity: [error, warn]}
    - {name: tank, pool: tank}
dashboard: {panels: [health, events, io, arc]}
keys: {select: [enter, space], left: h}
colors: {accent: "#7C9CB4", warn: {dark: "#D2B26E", light: "#86651F"}}
notifications:
  repeat: 24h
  channels:
    - {name: phone, type: ntfy, url: "https://ntfy.example.org", topic: zfs, min_severity: warning}
  templates:
    - {name: tight, metric: free, op: "<", value: 500G, text: "{pool} has {value} left"}
  alerts:
    - {template: capacity, pools: [tank], channels: phone, value: "70", repeat: 12h, resolved: false}
    - {template: tight}
schedules: {check_every: 15m, cron_time: "03:00"}
tune: {ghost_share: 0.25}
`
	if _, problems := Parse("good.yaml", []byte(good)); len(problems) > 0 {
		t.Fatalf("fstui refuses the good file: %v", problems)
	}
	if errs := validate(schema(), yamlValue(t, good), ""); len(errs) > 0 {
		t.Errorf("the schema refuses the good file: %v", errs)
	}
	for _, bad := range []string{
		"mosue: true\n",
		"thresholds: {capacity_warn: 120}\n",
		"ui: {start_view: home}\n",
		"notifications: {channels: [{name: Phone, type: ntfy}]}\n",
		"notifications: {channels: [{name: p, type: pager}]}\n",
		"notifications: {alerts: [{pools: [tank]}]}\n",
		"notifications: {alerts: [{template: capacity, colour: red}]}\n",
		"notifications: {templates: [{name: x, metric: bogus, value: \"1\", text: t}]}\n",
		"schedules: {cron_time: \"25:00\"}\n",
		"colors: {accent: blue}\n",
		"dashboard: {panels: [clock]}\n",
		"refresh: {pools: 1}\n",
	} {
		_, problems := Parse("bad.yaml", []byte(bad))
		errs := validate(schema(), yamlValue(t, bad), "")
		if len(problems) == 0 || len(errs) == 0 {
			t.Errorf("%q: fstui found %d problems, the schema %d", strings.TrimSpace(bad), len(problems), len(errs))
		}
	}
}

func yamlValue(t *testing.T, doc string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// validate checks v against the parts of JSON schema that schema uses.
func validate(s obj, v any, at string) []string {
	var errs []string
	if alts, ok := s["oneOf"].([]any); ok {
		n := 0
		for _, alt := range alts {
			if len(validate(alt.(obj), v, at)) == 0 { //nolint:forcetypeassert // built by schema
				n++
			}
		}
		if n != 1 {
			errs = append(errs, fmt.Sprintf("%s: matches %d of oneOf", at, n))
		}
	}
	if enum, ok := s["enum"]; ok && !slices.ContainsFunc(toSlice(enum), func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) && sameKind(e, v) }) {
		errs = append(errs, fmt.Sprintf("%s: %v is not one of %v", at, v, enum))
	}
	check := map[any]func(obj, any, string) []string{
		"object": validateObject, "array": validateArray, "integer": validateNumber, "number": validateNumber,
		"boolean": validateBool, "string": validateString,
	}[s["type"]]
	if check != nil {
		errs = append(errs, check(s, v, at)...)
	}
	return errs
}

func validateObject(s obj, v any, at string) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return []string{at + ": want a mapping"}
	}
	var errs []string
	for _, r := range toSlice(s["required"]) {
		if _, ok := m[fmt.Sprint(r)]; !ok {
			errs = append(errs, fmt.Sprintf("%s: %v is required", at, r))
		}
	}
	props, _ := s["properties"].(obj)
	for k, val := range m {
		sub, known := props[k].(obj)
		if !known {
			extra, ok := s["additionalProperties"].(obj)
			if !ok {
				errs = append(errs, at+": unknown key "+k)
				continue
			}
			sub = extra
		}
		errs = append(errs, validate(sub, val, at+"."+k)...)
	}
	return errs
}

func validateArray(s obj, v any, at string) []string {
	items, ok := v.([]any)
	if !ok {
		return []string{at + ": want a list"}
	}
	var errs []string
	for i, it := range items {
		errs = append(errs, validate(s["items"].(obj), it, fmt.Sprintf("%s[%d]", at, i))...) //nolint:forcetypeassert // built by schema
	}
	return errs
}

func validateNumber(s obj, v any, at string) []string {
	n, ok := toFloat(v)
	if !ok || s["type"] == "integer" && n != float64(int64(n)) {
		return []string{fmt.Sprintf("%s: want a %s, got %v", at, s["type"], v)}
	}
	if lo, ok := toFloat(s["minimum"]); ok && n < lo {
		return []string{fmt.Sprintf("%s: %v is below %v", at, v, lo)}
	}
	if hi, ok := toFloat(s["maximum"]); ok && n > hi {
		return []string{fmt.Sprintf("%s: %v is above %v", at, v, hi)}
	}
	return nil
}

func validateBool(_ obj, v any, at string) []string {
	if _, ok := v.(bool); !ok {
		return []string{at + ": want true or false"}
	}
	return nil
}

func validateString(s obj, v any, at string) []string {
	str, ok := v.(string)
	if !ok {
		return []string{fmt.Sprintf("%s: want text, got %v", at, v)}
	}
	if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(str) {
		return []string{fmt.Sprintf("%s: %q does not match %s", at, str, p)}
	}
	return nil
}

func toSlice(v any) []any {
	switch l := v.(type) {
	case []any:
		return l
	case []string:
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}
		return out
	}
	return nil
}

func sameKind(a, b any) bool {
	_, as := a.(string)
	_, bs := b.(string)
	return as == bs
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

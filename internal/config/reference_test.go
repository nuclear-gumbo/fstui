// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

const (
	referenceFile  = "../../docs/configuration.md"
	referenceStart = "<!-- settings reference: made from internal/config/settings.go by make docs -->\n"
	referenceEnd   = "<!-- end of settings reference -->\n"
)

// The settings reference in the docs is the registry's: a setting added
// without its documentation fails here. FSTUI_UPDATE_DOCS=1 rewrites it.
func TestReferenceIsCurrent(t *testing.T) {
	data, err := os.ReadFile(referenceFile)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	before, rest, ok := strings.Cut(doc, referenceStart)
	_, after, ok2 := strings.Cut(rest, referenceEnd)
	if !ok || !ok2 {
		t.Fatalf("%s: no settings reference markers", referenceFile)
	}
	want := before + referenceStart + "\n" + reference() + "\n" + referenceEnd + after
	if doc == want {
		return
	}
	if os.Getenv("FSTUI_UPDATE_DOCS") != "" {
		if err := os.WriteFile(referenceFile, []byte(want), 0o644); err != nil { //nolint:gosec // a documentation file
			t.Fatal(err)
		}
		return
	}
	t.Errorf("%s is out of date with the settings registry; run make docs", referenceFile)
}

// reference is the settings reference in docs/configuration.md: one
// table per section, made from the registry so the two cannot differ. A
// test compares them; make docs rewrites the file.
func reference() string {
	d := Default()
	var b strings.Builder
	section := "-"
	for _, s := range Settings(nil) {
		head, _, nested := strings.Cut(s.Path, ".")
		if !nested {
			head = ""
		}
		if head != section {
			section = head
			title := "General"
			if head != "" {
				title = "`" + head + ":`"
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "#### %s\n\n| Setting | Default | Accepts | Meaning |\n|---|---|---|---|\n", title)
		}
		desc := s.Desc
		if s.Restart {
			desc += "; applies after a restart"
		}
		value := "`" + s.get(&d) + "`"
		if value == "``" {
			value = "empty"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", s.Path, value, accepts(s), desc)
	}
	return b.String()
}

// accepts is a setting's values, short enough for a table cell.
func accepts(s Spec) string {
	switch s.Kind {
	case KindInt:
		return fmt.Sprintf("%d to %d", s.Min, s.Max)
	case KindSeconds:
		return fmt.Sprintf("%d to %d seconds", s.Min, s.Max)
	case KindDuration:
		return fmt.Sprintf("%s to %s", fmtDuration(s.MinDur), fmtDuration(s.MaxDur))
	case KindFloat:
		return fmt.Sprintf("%g to %g", s.MinFloat, s.MaxFloat)
	case KindBool:
		return "`true`, `false`"
	case KindString:
		return s.Want
	case KindChoice:
	}
	if s.Choices == nil {
		return "a theme name"
	}
	return "`" + strings.Join(s.Choices, "`, `") + "`"
}

// SPDX-License-Identifier: GPL-3.0-or-later

// Package uitest compares rendered views with golden files.
//
// Golden files live in the calling package's testdata directory, one per
// test, named after the test. Run the tests with -update-golden to write
// them from the current output, then review the diff before committing.
package uitest

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update-golden", false, "rewrite golden files from the current output")

// Golden fails t if got differs from testdata/<test name>.golden. Escape
// sequences are written as \x1b so the files stay readable in review.
func Golden(t *testing.T, got string) {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	path := filepath.Join("testdata", name+".golden")
	got = strings.ReplaceAll(got, "\x1b", `\x1b`)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // testdata/<test name>.golden
	if err != nil {
		t.Fatalf("%v (run with -update-golden to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the output:\n%s", path, diff(string(want), got))
	}
}

// diff lists the lines that differ, with their line numbers.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			fmt.Fprintf(&b, "line %d\n  want: %s\n  got:  %s\n", i+1, wl, gl)
		}
	}
	return b.String()
}

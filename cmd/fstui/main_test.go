// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"
)

// The command line follows GNU conventions: long options with two dashes, short
// ones with one, help on stdout with exit 0, usage errors on stderr with exit 2.
func TestCommandLine(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		code       int
		out, errOK string
	}{
		{[]string{"--help"}, 0, "  -h, --help", ""},
		{[]string{"-h"}, 0, "      --list-themes", ""},
		{[]string{"--list-themes"}, 0, "shipped: default, catppuccin", ""},
		{[]string{"-version"}, exitUsage, "", "Try 'fstui --help'"},
		{[]string{"--bogus"}, exitUsage, "", "unknown flag: --bogus"},
		{[]string{"extra"}, exitUsage, "", `unexpected argument "extra"`},
	} {
		var out, errb bytes.Buffer
		code := run(tc.args, &out, &errb)
		if code != tc.code {
			t.Errorf("%v: exit %d, want %d (stderr %q)", tc.args, code, tc.code, errb.String())
		}
		if tc.out != "" && !strings.Contains(out.String(), tc.out) {
			t.Errorf("%v: stdout %q lacks %q", tc.args, out.String(), tc.out)
		}
		if tc.errOK != "" && !strings.Contains(errb.String(), tc.errOK) {
			t.Errorf("%v: stderr %q lacks %q", tc.args, errb.String(), tc.errOK)
		}
		if tc.code == 0 && errb.Len() > 0 {
			t.Errorf("%v: unexpected stderr %q", tc.args, errb.String())
		}
	}
}

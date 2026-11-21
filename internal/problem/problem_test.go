// SPDX-License-Identifier: GPL-3.0-or-later

package problem

import "testing"

func TestSeverityString(t *testing.T) {
	if Info.String() != "info" || Warning.String() != "warning" || Error.String() != "error" {
		t.Error("severity words")
	}
}

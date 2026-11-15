// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// zfsWords are ZFS terms that must not leak into the neutral package. Whole
// words only, so "search" does not match "arc".
var zfsWords = regexp.MustCompile(`(?i)\b(vdevs?|zpools?|zfs|datasets?|zvols?|arc|l2arc|raidz\d*)\b`)

func TestNoZFSVocabulary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "vocabulary_test.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := zfsWords.FindAll(src, -1); m != nil {
			t.Errorf("%s uses ZFS vocabulary: %q", f, m)
		}
	}
}

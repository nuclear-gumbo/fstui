// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The parsers read output that names, property values and file names
// written by any user with delegated rights flow into, and they run in
// root sessions. Whatever the input, they must return, not panic.

// seed adds recorded output to a fuzz corpus.
func seed(f *testing.F, files ...string) {
	f.Helper()
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "zfs", "2.3.9", name)) // test data
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
}

func FuzzJSONParsers(f *testing.F) {
	seed(f, "healthy/zpool-list.json", "healthy/zpool-get-p.json", "healthy/zfs-list.json",
		"healthy/zfs-get-p.json", "healthy/zpool-status.json", "zfs-version.json", "roles/zpool-status.json")
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = ParsePoolList(b)
		_, _ = ParsePoolGet(b)
		_, _ = ParseDatasetList(b)
		_, _ = ParseDatasetGet(b)
		_, _ = ParsePoolStatus(b)
		_, _ = ParseActivity(b, "tank")
		_, _ = ParseVersion(b)
		_, _ = ParseScrubTimers(b)
	})
}

func FuzzTextParsers(f *testing.F) {
	seed(f, "healthy/zpool-history.txt", "zfs-version.txt", "events/induced-v.txt",
		"allow/nested.txt", "arcstats/l2arc.txt", "iostat/four-pools.txt", "import/zpool-import-missing.txt", "tune/interval/zpool-iostat-l.txt", "tune/1-seqwrite/txgs")
	f.Add([]byte("M\t/tank/a\\0040b\t/tank/c\n+\tF\t/x\\0033\n"))
	f.Add([]byte("destroy\ttank/a@1\nreclaim\t1024\n"))
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = ParseHistory(b)
		_, _ = ParseARCStats(b)
		_, _ = ParseDestroyDryRun(b)
		_, _, _ = ParseDiff(b)
		_, _ = ParseAllow(b)
		_, _, _ = ParseEvents(b)
		_, _ = ParseVersionText(b)
		_, _ = ParseSendEstimate(b)
		_, _ = ParseImportable(b)
		_, _ = ParseKstat(b)
		_, _ = ParseTxgs(b)
		p := IOStatParser{Sample: func(IOSample) {}}
		for _, l := range bytes.Split(b, []byte("\n")) {
			if p.Line(string(l)) != nil {
				break
			}
		}
	})
}

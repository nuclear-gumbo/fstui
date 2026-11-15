// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package audit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

func TestRecentNewestFirst(t *testing.T) {
	l, path := open(t)
	for i := 1; i <= 3; i++ {
		if err := l.Record(execute.StepEvent{Mode: execute.Write, Step: i, Phase: execute.Attempt}); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{\"time\":\"cut off\n")
	_ = f.Close()
	recs, skipped, err := Recent(filepath.Dir(path), 2)
	if err != nil || skipped != 1 || len(recs) != 2 || recs[0].Step != 3 || recs[1].Step != 2 {
		t.Fatalf("Recent = %+v, %d, %v", recs, skipped, err)
	}
	if recs, _, err := Recent(t.TempDir(), 10); err != nil || recs != nil {
		t.Fatalf("missing log: %v, %v", recs, err)
	}
}

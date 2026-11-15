// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// maxRead bounds how much of the end of the log is read for the view.
const maxRead = 4 << 20

// Recent returns up to n records from the end of the audit log in dir,
// newest first. A missing log is no records. Lines that are not records
// (a crash mid-write, a damaged file) are counted, not returned.
func Recent(dir string, n int) (recs []Record, skipped int, err error) {
	f, err := os.Open(filepath.Join(dir, DefaultFile)) //nolint:gosec // the directory fstui chose for its log
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("audit log: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("audit log: %w", err)
	}
	start := max(fi.Size()-maxRead, 0)
	data := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(data, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, fmt.Errorf("audit log: %w", err)
	}
	if start > 0 { // the first line is probably cut
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	for line := range bytes.Lines(data) {
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			skipped++
			continue
		}
		recs = append(recs, r)
	}
	slices.Reverse(recs)
	if len(recs) > n {
		recs = recs[:n]
	}
	return recs, skipped, nil
}

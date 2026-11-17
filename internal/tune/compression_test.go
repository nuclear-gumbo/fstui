// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompression(t *testing.T) {
	gib := uint64(1 << 30)
	cases := []struct {
		name    string
		ds      []DatasetCompression
		walk    *WalkResult
		verdict Verdict
		want    string
	}{
		{"zstd on video", []DatasetCompression{{"tank/video", "zstd", 1.01, 50 * gib, 51 * gib}}, nil, WorthALook, "tank/video barely compresses; lz4"},
		{"lz4 on video", []DatasetCompression{{"tank/video", "lz4", 1.01, 50 * gib, 51 * gib}}, nil, Fits, "c measures"},
		{"text", []DatasetCompression{{"tank/logs", "zstd", 3.2, 2 * gib, 6 * gib}}, nil, Fits, "3.20x with zstd"},
		{"walk", []DatasetCompression{{"tank/media", "lz4", 1.2, 10 * gib, 12 * gib}}, &WalkResult{Files: 10, Bytes: 100, Poor: 80, Dirs: map[string]uint64{}}, WorthALook, "80% of the data"},
		{"small", []DatasetCompression{{"tank/a", "zstd", 1.0, 10 << 20, 10 << 20}}, nil, NotEnough, "holds"},
	}
	for _, c := range cases {
		f := Compression(c.ds, c.walk, bytesOnly)
		if f.Verdict != c.verdict || !strings.Contains(f.Figure+" "+f.Points, c.want) {
			t.Errorf("%s: %s, %q, %q", c.name, f.Verdict.Word(), f.Figure, f.Points)
		}
	}
}

// A written file allocates its size (it does not compress, as on any
// filesystem without compression); a sparse one allocates nothing and
// looks compressed; a symbolic link is not followed.
func TestWalk(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 256<<10)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "media", "random"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Symlink("/etc", filepath.Join(dir, "etc")); err != nil {
		t.Fatal(err)
	}
	r := Walk(t.Context(), []string{dir})
	if r.Files != 2 || r.Bytes != 256<<10+1<<20 || r.Poor != 256<<10 || r.Dirs[filepath.Join(dir, "media")] != 256<<10 {
		t.Errorf("walk %+v", r)
	}
	lines := strings.Join(r.lines(bytesOnly), "\n")
	if !strings.Contains(lines, "that does not compress in "+filepath.Join(dir, "media")) || !strings.Contains(lines, "sparse files") {
		t.Errorf("lines:\n%s", lines)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if r := Walk(ctx, []string{dir}); r.Stopped != "cancelled" {
		t.Errorf("cancelled walk: %+v", r)
	}
}

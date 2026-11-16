// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration

package zfs

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

// zfs send accepts "--" before the snapshot, and its estimates add up:
// a chain of increments is what -I estimates for the same range, and a
// compressed stream of compressible data is smaller.
func TestIntegrationSendEstimate(t *testing.T) {
	p := testpool.New(t, "", 1)
	p.SetProperty("", "compression", "lz4")
	mnt := filepath.Join(testpool.Dir, "mnt", p.Name)
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(mnt, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
		p.Sync()
	}
	write("text", bytes.Repeat([]byte("compressible line for the send size test\n"), 200000))
	s1 := p.Snapshot("", "s1")
	random := make([]byte, 4<<20)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	write("random", random)
	s2 := p.Snapshot("", "s2")
	write("more", bytes.Repeat([]byte("more text\n"), 100000))
	s3 := p.Snapshot("", "s3")

	r := NewReader(runner(t))
	est := func(s SendStep, o SendOptions) SendEstimate {
		t.Helper()
		e, err := r.EstimateSend(t.Context(), s, o)
		if err != nil {
			t.Fatalf("%v: %v", s, err)
		}
		return e
	}
	full := est(SendStep{To: s1}, SendOptions{})
	compressed := est(SendStep{To: s1}, SendOptions{Compressed: true})
	if full.Total == 0 || compressed.Total >= full.Total {
		t.Errorf("full %d, compressed %d", full.Total, compressed.Total)
	}
	a := est(SendStep{From: s1, To: s2}, SendOptions{})
	b := est(SendStep{From: s2, To: s3}, SendOptions{})
	between := est(SendStep{From: s1, To: s3}, SendOptions{Intermediates: true})
	if len(between.Streams) != 2 || between.Total != a.Total+b.Total {
		t.Errorf("-I %+v, increments %d + %d", between, a.Total, b.Total)
	}
	if a.Total < 4<<20 {
		t.Errorf("the increment holding 4 MiB of random data is %d bytes", a.Total)
	}
}

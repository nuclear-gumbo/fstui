// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"path/filepath"
	"testing"
	"time"
)

// The seq dataset read back its 200 MiB file in 4 KiB reads in the last
// phase of the recording.
func TestParseKstatObjset(t *testing.T) {
	files, err := filepath.Glob("../../testdata/zfs/2.3.9/tune/6-reads/objset-*")
	if err != nil || len(files) == 0 {
		t.Fatalf("recordings: %v, %v", files, err)
	}
	found := false
	for _, f := range files {
		k, err := ParseKstat(golden(t, "tune", "6-reads", filepath.Base(f)))
		if err != nil {
			t.Fatal(err)
		}
		if k.String("dataset_name") != "fstuitest_tune/seq" {
			continue
		}
		found = true
		if n, ok := k.Uint("nread"); !ok || n != 200<<20 {
			t.Errorf("nread %d, %v", n, ok)
		}
		if n, ok := k.Uint("reads"); !ok || n != 51200 {
			t.Errorf("reads %d, %v", n, ok)
		}
	}
	if !found {
		t.Error("no objset for fstuitest_tune/seq")
	}
	if _, ok := (Kstat{}).Uint("writes"); ok {
		t.Error("a missing counter is found")
	}
}

func TestParseTxgs(t *testing.T) {
	txgs, err := ParseTxgs(golden(t, "tune", "1-seqwrite", "txgs"))
	if err != nil || len(txgs) < 3 {
		t.Fatalf("%d txgs, %v", len(txgs), err)
	}
	var synced time.Duration
	for _, x := range txgs {
		if x.State == "C" {
			synced += time.Duration(x.SyncTime) // recorded ns
		}
	}
	if synced == 0 {
		t.Error("committed txgs took no time to sync while 200 MiB was written")
	}
	if _, err := ParseTxgs([]byte("txg birth\n1 2\n")); err == nil {
		t.Error("a table without the columns is accepted")
	}
}

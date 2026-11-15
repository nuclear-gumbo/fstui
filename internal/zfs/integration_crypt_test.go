// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_integration && !fstui_nowrite

package zfs_test

import (
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/testpool"
)

func TestIntegrationEncryption(t *testing.T) {
	p := testpool.New(t, "", 1)
	b, pl, _, _ := writeSetup(t)
	ctx := t.Context()
	sec := p.Name + "/sec"
	cr, err := b.EncryptedCreate(ctx, sec, nil, []byte("correct horse"))
	mustRun(t, pl, cr, err)
	if len(cr.Stdin) != 0 && cr.Stdin[0] != 0 {
		t.Error("the passphrase was not cleared after running")
	}
	p.CreateFilesystem("sec/in")
	keyStatus := func() string {
		f, err := b.R.Get(ctx, []string{sec, sec + "/in"}, []string{"keystatus", "mounted"})
		if err != nil {
			t.Fatal(err)
		}
		return f[sec]["keystatus"] + " " + f[sec]["mounted"] + " " + f[sec+"/in"]["mounted"]
	}
	if got := keyStatus(); got != "available yes yes" {
		t.Fatalf("after create: %s", got)
	}
	un, err := b.KeyUnload(ctx, sec)
	mustRun(t, pl, un, err)
	if got := keyStatus(); got != "unavailable no no" {
		t.Fatalf("after unload: %s", got)
	}
	wrong, err := b.KeyLoad(ctx, sec, []byte("wrong passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Run(ctx, wrong); err == nil {
		t.Fatal("a wrong passphrase loaded the key")
	}
	load, err := b.KeyLoad(ctx, sec, []byte("correct horse"))
	mustRun(t, pl, load, err)
	if got := keyStatus(); got != "available yes yes" {
		t.Fatalf("after load: %s", got)
	}
	ch, err := b.KeyChange(ctx, sec, []byte("battery staple"))
	mustRun(t, pl, ch, err)
	un, err = b.KeyUnload(ctx, sec)
	mustRun(t, pl, un, err)
	old, _ := b.KeyLoad(ctx, sec, []byte("correct horse"))
	if _, err := pl.Run(ctx, old); err == nil {
		t.Fatal("the old passphrase still works after change")
	}
	load, err = b.KeyLoad(ctx, sec, []byte("battery staple"))
	mustRun(t, pl, load, err)
}

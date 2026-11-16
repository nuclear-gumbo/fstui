// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"errors"
	"strings"
	"testing"

	"github.com/nuclear-gumbo/fstui/internal/mutate"
	"github.com/nuclear-gumbo/fstui/internal/zfs"
)

func cryptWorld(status string) Builder {
	r := dsWorld()
	r.ids["tank/sec"] = zfs.Identity{GUID: "30", CreateTXG: "30"}
	r.props["tank/sec"] = map[string]string{
		"keystatus": status, "encryptionroot": "tank/sec", "keylocation": "prompt",
		"mounted": "yes", "canmount": "on", "mountpoint": "/tank/sec", "type": "filesystem",
	}
	r.props["tank/sec/in"] = map[string]string{
		"keystatus": status, "encryptionroot": "tank/sec", "keylocation": "none",
		"mounted": "yes", "canmount": "on", "mountpoint": "/tank/sec/in", "type": "filesystem",
	}
	r.kids = map[string][]string{"tank/sec": {"tank/sec/in"}}
	return Builder{R: r}
}

func TestEncryptedCreateKeepsThePassphraseOutOfArgv(t *testing.T) {
	secret := []byte("correct horse")
	p, err := cryptWorld("available").EncryptedCreate(t.Context(), "tank/new", nil, secret)
	if err != nil {
		t.Fatal(err)
	}
	argv := p.Steps[0].String()
	if strings.Contains(argv, "horse") || !strings.Contains(argv, "keyformat=passphrase") || !strings.Contains(argv, "keylocation=prompt") {
		t.Errorf("argv %q", argv)
	}
	if string(p.Stdin) != "correct horse\n" || !p.Secret {
		t.Errorf("stdin %q, secret %v", p.Stdin, p.Secret)
	}
	for k, v := range p.Params {
		if strings.Contains(v, "horse") {
			t.Errorf("param %s carries the passphrase", k)
		}
	}
	secret[0] = 'X' // the plan has its own copy
	if p.Stdin[0] != 'c' {
		t.Error("the plan shares the caller's buffer")
	}
	if _, err := cryptWorld("available").EncryptedCreate(t.Context(), "tank/new", nil, []byte("short")); err == nil {
		t.Error("a 5-character passphrase was accepted")
	}
	if _, err := cryptWorld("available").Build(t.Context(), mutate.DatasetCreate, p.Params); !errors.Is(err, mutate.ErrSecretNotSaved) {
		t.Errorf("rebuilding an encrypted create: %v", err)
	}
}

func TestKeyUnloadUnmountsDeepestFirst(t *testing.T) {
	p, err := cryptWorld("available").KeyUnload(t.Context(), "tank/sec")
	if err != nil || len(p.Blocked) != 0 {
		t.Fatalf("unload: %+v, %v", p, err)
	}
	var got []string
	for _, s := range p.Steps {
		got = append(got, s.String())
	}
	want := "zfs unmount -- tank/sec/in | zfs unmount -- tank/sec | zfs unload-key -- tank/sec"
	if strings.Join(got, " | ") != want || mutate.TierOf(p) != mutate.Medium {
		t.Errorf("steps %q, tier %s", got, mutate.TierOf(p))
	}
	if p, _ := cryptWorld("available").KeyUnload(t.Context(), "tank/sec/in"); len(p.Blocked) == 0 {
		t.Error("unloading below the encryption root was not blocked")
	}
}

func TestKeyLoadAndChange(t *testing.T) {
	load, err := cryptWorld("unavailable").KeyLoad(t.Context(), "tank/sec", []byte("correct horse"))
	if err != nil || len(load.Blocked) != 0 || load.Steps[0].String() != "zfs load-key -- tank/sec" || len(load.Steps) != 3 {
		t.Fatalf("load: %+v, %v", load, err)
	}
	if p, _ := cryptWorld("available").KeyLoad(t.Context(), "tank/sec", []byte("correct horse")); len(p.Blocked) == 0 {
		t.Error("loading a loaded key was not blocked")
	}
	ch, err := cryptWorld("available").KeyChange(t.Context(), "tank/sec", []byte("battery staple"))
	if err != nil || len(ch.Blocked) != 0 || string(ch.Stdin) != "battery staple\n" || len(ch.Irreversible) == 0 {
		t.Fatalf("change: %+v, %v", ch, err)
	}
}

// Only the step that reads the passphrase is marked to receive it: a
// mount after load-key must not get it on its standard input.
func TestOnlyThePassphraseStepGetsStdin(t *testing.T) {
	load, err := cryptWorld("unavailable").KeyLoad(t.Context(), "tank/sec", []byte("correct horse"))
	if err != nil {
		t.Fatal(err)
	}
	var marked []string
	for _, s := range load.Steps {
		if s.Stdin {
			marked = append(marked, s.String())
		}
	}
	if len(marked) != 1 || !strings.Contains(marked[0], "load-key") || len(load.Steps) < 2 {
		t.Errorf("steps %d, marked %q", len(load.Steps), marked)
	}
}

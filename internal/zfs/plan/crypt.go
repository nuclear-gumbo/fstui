// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package plan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/mutate"
)

// Encryption: create an encrypted filesystem, load, unload and change a key.
// Passphrases go to zfs on stdin, read once from a pipe on 2.3.9; they never
// reach argv, Params or the audit log, and the pipeline zeroes them after
// running.

// minPassphrase is zfs's minimum (2.3.9: "Passphrase too short (min 8)").
const minPassphrase = 8

// stdinFor checks a passphrase and returns it as zfs reads it, in a
// new buffer the plan owns.
func stdinFor(secret []byte) ([]byte, error) {
	switch {
	case len(secret) < minPassphrase:
		return nil, fmt.Errorf("the passphrase must be at least %d characters", minPassphrase)
	case bytes.ContainsAny(secret, "\n\r\x00"):
		return nil, errors.New("the passphrase cannot contain a line break or NUL")
	}
	out := make([]byte, 0, len(secret)+1)
	return append(append(out, secret...), '\n'), nil
}

// EncryptedCreate plans a filesystem with its own passphrase.
func (b Builder) EncryptedCreate(ctx context.Context, ds string, props map[string]string, secret []byte) (*mutate.Plan, error) {
	stdin, err := stdinFor(secret)
	if err != nil {
		return nil, err
	}
	p, err := b.DatasetCreate(ctx, ds, props)
	if err != nil {
		clear(stdin)
		return nil, err
	}
	a := &p.Steps[0]
	a.Options = slices.Insert(a.Options, 1, "-o", "encryption=on", "-o", "keyformat=passphrase", "-o", "keylocation=prompt")
	p.Summary = "Create encrypted filesystem " + show(ds)
	p.Params["encrypted"] = "true"
	p.Stdin, p.Secret = stdin, true
	affects(p, "its data is encrypted (aes-256-gcm) with a key made from the passphrase")
	p.Irreversible = []string{"Without the passphrase the data cannot be read by anyone, including you."}
	return p, nil
}

// cryptFacts are the encryption properties of a dataset.
type cryptFacts struct{ status, root, location string }

func (b Builder) cryptFacts(ctx context.Context, ds string) (cryptFacts, error) {
	f, err := b.R.Get(ctx, []string{ds}, []string{"keystatus", "encryptionroot", "keylocation"})
	if err != nil {
		return cryptFacts{}, err
	}
	return cryptFacts{f[ds]["keystatus"], f[ds]["encryptionroot"], f[ds]["keylocation"]}, nil
}

// blockNotRoot blocks a key change asked of a dataset that is not its
// encryption root: zfs refuses ("Keys must be unloaded for encryption
// root of ...").
func blockNotRoot(p *mutate.Plan, ds string, c cryptFacts) {
	switch {
	case c.root == "" || c.root == "-" || c.status == "-":
		p.Blocked = append(p.Blocked, show(ds)+" is not encrypted")
	case c.root != ds:
		p.Blocked = append(p.Blocked, "its key belongs to "+show(c.root)+"; change it there")
	}
}

// sharing lists ds and the filesystems below it that use its key, with
// whether each is mounted and should be, deepest last.
func (b Builder) sharing(ctx context.Context, ds string) ([]string, map[string]map[string]string, error) {
	below, err := b.R.Descendants(ctx, ds)
	if err != nil {
		return nil, nil, err
	}
	all := append([]string{ds}, below...)
	facts, err := b.R.Get(ctx, all, []string{"encryptionroot", "mounted", "canmount", "mountpoint", "type"})
	if err != nil {
		return nil, nil, err
	}
	var out []string
	for _, n := range all {
		if facts[n]["encryptionroot"] == ds && facts[n]["type"] == "filesystem" {
			out = append(out, n)
		}
	}
	return out, facts, nil
}

// KeyLoad plans "zfs load-key", then mounts the filesystems that use the
// key (as root; other users cannot mount on Linux).
func (b Builder) KeyLoad(ctx context.Context, ds string, secret []byte) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	c, err := b.cryptFacts(ctx, ds)
	if err != nil {
		return nil, err
	}
	p := newPlan(mutate.KeyLoad, "Load the key of "+show(ds), []string{ds}, map[string]string{"dataset": ds},
		zfsArgv([]string{"load-key"}, ds))
	blockNotRoot(p, ds, c)
	blockIf(p, c.status == "available", "its key is already loaded")
	if c.location == "prompt" {
		if p.Stdin, err = stdinFor(secret); err != nil {
			return nil, err
		}
		p.Secret = true
		affects(p, "a wrong passphrase changes nothing; zfs says so")
	} else {
		affects(p, "the key is read from "+show(c.location))
	}
	names, facts, err := b.sharing(ctx, ds)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		f := facts[n]
		if !b.Unprivileged && f["canmount"] == "on" && f["mountpoint"] != "none" && f["mountpoint"] != "legacy" {
			p.Steps = append(p.Steps, zfsArgv([]string{"mount"}, n))
			affects(p, "mounts "+n+" at "+f["mountpoint"])
		}
	}
	return p, b.watchKey(ctx, p, ds)
}

// KeyUnload plans unmounting every filesystem that uses the key, deepest
// first, then "zfs unload-key": zfs refuses while any is mounted.
func (b Builder) KeyUnload(ctx context.Context, ds string) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	c, err := b.cryptFacts(ctx, ds)
	if err != nil {
		return nil, err
	}
	p := newPlan(mutate.KeyUnload, "Unload the key of "+show(ds), []string{ds}, map[string]string{"dataset": ds})
	blockNotRoot(p, ds, c)
	blockIf(p, c.status != "available", "its key is not loaded")
	names, facts, err := b.sharing(ctx, ds)
	if err != nil {
		return nil, err
	}
	for _, n := range slices.Backward(names) {
		if facts[n]["mounted"] == "yes" {
			p.Steps = append(p.Steps, zfsArgv([]string{"unmount"}, n))
			affects(p, "unmounts "+n+"; programs using files there lose them")
		}
	}
	p.Steps = append(p.Steps, zfsArgv([]string{"unload-key"}, ds))
	affects(p, "its data cannot be read until the key is loaded again with the passphrase")
	blockIf(p, b.Unprivileged && len(p.Steps) > 1, "unmounting needs root on Linux; unmount as root first")
	return p, b.watchKey(ctx, p, ds)
}

// KeyChange plans "zfs change-key" to a new passphrase.
func (b Builder) KeyChange(ctx context.Context, ds string, secret []byte) (*mutate.Plan, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	stdin, err := stdinFor(secret)
	if err != nil {
		return nil, err
	}
	c, err := b.cryptFacts(ctx, ds)
	if err != nil {
		clear(stdin)
		return nil, err
	}
	p := newPlan(mutate.KeyChange, "Change the passphrase of "+show(ds), []string{ds}, map[string]string{"dataset": ds},
		execute.Argv{Tool: execute.ZFS, Options: []string{"change-key", "-o", "keyformat=passphrase", "-o", "keylocation=prompt"}, Operands: []string{ds}})
	p.Stdin, p.Secret = stdin, true
	blockNotRoot(p, ds, c)
	blockIf(p, c.status != "available", "its key must be loaded to change it")
	affects(p, "the data is not re-encrypted; only the key that protects it changes")
	p.Irreversible = []string{"The old passphrase stops working. Without the new one the data cannot be read."}
	return p, b.watchKey(ctx, p, ds)
}

// watchKey fingerprints the dataset and its key status.
func (b Builder) watchKey(ctx context.Context, p *mutate.Plan, ds string) error {
	return b.watchWith(ctx, p, []string{ds}, func(ctx context.Context) (mutate.Fingerprint, error) {
		c, err := b.cryptFacts(ctx, ds)
		if err != nil {
			return nil, err
		}
		return mutate.Fingerprint{ds + " (key)": c.status}, nil
	})
}

// KeyAvailability says, for load, unload and change, why each cannot be
// done on ds now; "" where it can.
func (b Builder) KeyAvailability(ctx context.Context, ds string) (map[mutate.Op]string, error) {
	if err := parseDS(ds); err != nil {
		return nil, err
	}
	c, err := b.cryptFacts(ctx, ds)
	if err != nil {
		return nil, err
	}
	probe := &mutate.Plan{}
	blockNotRoot(probe, ds, c)
	base := ""
	if len(probe.Blocked) > 0 {
		base = probe.Blocked[0]
	}
	out := map[mutate.Op]string{mutate.KeyLoad: base, mutate.KeyUnload: base, mutate.KeyChange: base}
	if base == "" {
		if c.status == "available" {
			out[mutate.KeyLoad] = "its key is already loaded"
		} else {
			out[mutate.KeyUnload] = "its key is not loaded"
			out[mutate.KeyChange] = "its key must be loaded to change it"
		}
	}
	return out, nil
}

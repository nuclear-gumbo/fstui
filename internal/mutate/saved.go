// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !fstui_nowrite

package mutate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// SavedFormat is the version of the saved plan file format.
const SavedFormat = 1

// Saved is a plan stored to apply later. It is data, not a script: Steps are
// kept to show what the plan was, and are never run. Applying builds the plan
// again from Op and Params and refuses if the state differs from Baseline.
type Saved struct {
	Format   int               `json:"format"`
	Op       string            `json:"op"`
	Params   map[string]string `json:"params"`
	Summary  string            `json:"summary"`
	Tier     string            `json:"tier"`
	Baseline map[string]string `json:"baseline"`
	Steps    [][]string        `json:"steps"`
	Created  string            `json:"created"` // RFC 3339, UTC
	UID      int               `json:"uid"`
	SudoUser string            `json:"sudo_user,omitempty"`
	Host     string            `json:"host"`
	Version  string            `json:"fstui_version"`
}

// SavedState is what became of a saved plan.
type SavedState struct {
	State  string `json:"state"` // applied, refused
	Time   string `json:"time"`
	Reason string `json:"reason,omitempty"`
}

// Entry is one saved plan as listed.
type Entry struct {
	SHA256 string
	Saved  Saved
	State  SavedState // State.State is "saved" until applied or refused
	Err    error      // the file could not be read or does not match its name
}

// Store keeps saved plans in one directory, one file per plan named by
// the SHA-256 of its contents, and a state file beside it.
type Store struct {
	Dir     string
	Host    string
	Version string
	Now     func() time.Time
}

// ErrNotSaved is returned for a plan that cannot be saved.
var ErrNotSaved = errors.New("this change cannot be saved")

// ErrSecretNotSaved is returned for a plan that carries a passphrase.
var ErrSecretNotSaved = errors.New("a change that needs a passphrase cannot be saved; make it now instead")

// Save stores p and returns its SHA-256.
func (s Store) Save(p *Plan) (string, error) {
	if p.Params == nil || p.Op == EnableWrite {
		return "", ErrNotSaved
	}
	if p.Secret {
		return "", ErrSecretNotSaved
	}
	if err := s.dir(); err != nil {
		return "", err
	}
	sv := Saved{
		Format: SavedFormat, Op: p.Op.String(), Params: p.Params, Summary: p.Summary, Tier: TierOf(p).String(),
		Baseline: p.Baseline, Created: s.Now().UTC().Format(time.RFC3339),
		UID: os.Geteuid(), SudoUser: os.Getenv("SUDO_USER"), Host: s.Host, Version: s.Version,
	}
	for _, a := range p.Steps {
		sv.Steps = append(sv.Steps, append([]string{string(a.Tool)}, a.Args()...))
	}
	data, err := json.MarshalIndent(sv, "", "  ")
	if err != nil {
		return "", fmt.Errorf("saving the plan: %w", err)
	}
	data = append(data, '\n')
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if err := s.write(sha+".json", data); err != nil {
		return "", err
	}
	return sha, nil
}

// dir creates the directory 0700 and checks it belongs to the effective
// user and nobody else can write it, as for config files.
func (s Store) dir() error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("saved plans: %w", err)
	}
	fi, err := os.Lstat(s.Dir)
	if err != nil {
		return fmt.Errorf("saved plans: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Geteuid() || fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("saved plans: %s must be a directory owned by uid %d with no access for others", s.Dir, os.Geteuid())
	}
	return nil
}

// write writes a file atomically: a temporary file renamed into place.
func (s Store) write(name string, data []byte) error {
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("saved plans: %w", err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("saved plans: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.Dir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("saved plans: %w", err)
	}
	return nil
}

// validSHA reports whether s is a full lowercase hex SHA-256.
func validSHA(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// Load reads the saved plan named sha and checks its contents still
// hash to its name.
func (s Store) Load(sha string) (Saved, error) {
	if !validSHA(sha) {
		return Saved{}, fmt.Errorf("%q is not a plan's SHA-256", sha)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, sha+".json")) //nolint:gosec // sha is checked to be hex above
	if err != nil {
		return Saved{}, fmt.Errorf("saved plan: %w", err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != sha {
		return Saved{}, fmt.Errorf("saved plan %s was changed after it was saved", sha[:12])
	}
	var sv Saved
	if err := json.Unmarshal(data, &sv); err != nil {
		return Saved{}, fmt.Errorf("saved plan %s: %w", sha[:12], err)
	}
	if sv.Format != SavedFormat {
		return Saved{}, fmt.Errorf("saved plan %s has format %d; this fstui reads %d", sha[:12], sv.Format, SavedFormat)
	}
	return sv, nil
}

// List returns the saved plans, newest first.
func (s Store) List() ([]Entry, error) {
	files, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("saved plans: %w", err)
	}
	var out []Entry
	for _, f := range files {
		sha, ok := strings.CutSuffix(f.Name(), ".json")
		if !ok || !validSHA(sha) {
			continue
		}
		e := Entry{SHA256: sha, State: SavedState{State: "saved"}}
		e.Saved, e.Err = s.Load(sha)
		if st, err := s.state(sha); err == nil {
			e.State = st
		}
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b Entry) int { return strings.Compare(b.Saved.Created, a.Saved.Created) })
	return out, nil
}

func (s Store) state(sha string) (SavedState, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, sha+".state")) //nolint:gosec // callers pass names List checked
	if err != nil {
		return SavedState{}, fmt.Errorf("saved plan state: %w", err)
	}
	var st SavedState
	if err := json.Unmarshal(data, &st); err != nil {
		return SavedState{}, fmt.Errorf("saved plan state: %w", err)
	}
	return st, nil
}

// Mark records what became of a saved plan.
func (s Store) Mark(sha, state, reason string) error {
	if !validSHA(sha) {
		return fmt.Errorf("%q is not a plan's SHA-256", sha)
	}
	data, err := json.Marshal(SavedState{State: state, Time: s.Now().UTC().Format(time.RFC3339), Reason: reason})
	if err != nil {
		return fmt.Errorf("saved plan state: %w", err)
	}
	return s.write(sha+".state", append(data, '\n'))
}

// Delete removes a saved plan and its state.
func (s Store) Delete(sha string) error {
	if !validSHA(sha) {
		return fmt.Errorf("%q is not a plan's SHA-256", sha)
	}
	_ = os.Remove(filepath.Join(s.Dir, sha+".state"))
	if err := os.Remove(filepath.Join(s.Dir, sha+".json")); err != nil {
		return fmt.Errorf("saved plan: %w", err)
	}
	return nil
}

// Rebuilder builds a plan again from an operation and its parameters,
// against the current state; zfs/plan.Builder.Build is one.
type Rebuilder func(ctx context.Context, op Op, params map[string]string) (*Plan, error)

// Reapply rebuilds a saved plan. It refuses, with what changed, if the
// state now differs from the one the plan was made against. The plan
// returned carries the saved plan's SHA-256 for the audit log.
func Reapply(ctx context.Context, sha string, sv Saved, rebuild Rebuilder) (*Plan, error) {
	op, ok := OpNamed(sv.Op)
	if !ok {
		return nil, fmt.Errorf("saved plan %s: unknown operation %q", sha[:12], sv.Op)
	}
	p, err := rebuild(ctx, op, sv.Params)
	if err != nil {
		return nil, err
	}
	if d := Fingerprint(sv.Baseline).Diff(p.Baseline); len(d) > 0 {
		return nil, &StateChangedError{Changes: d, Since: "the plan was saved"}
	}
	p.SavedSHA256 = sha
	return p, nil
}

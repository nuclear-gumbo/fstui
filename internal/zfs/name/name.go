// SPDX-License-Identifier: GPL-3.0-or-later

// Package name validates ZFS pool, dataset, snapshot and bookmark names.
//
// Validated names are distinct types, so a string that was never checked cannot
// reach a zfs or zpool command line. The rules were confirmed against OpenZFS
// 2.3.9 on throwaway pools where possible; the begins-with-a-letter and
// reserved-name rules for pools come from zpool-create(8), because testing them
// would need unprefixed test pools.
package name

import (
	"errors"
	"fmt"
	"strings"
)

// MaxLen is the longest full name ZFS accepts (ZFS_MAX_DATASET_NAME_LEN - 1;
// tested: 255 accepted, 256 rejected).
const MaxLen = 255

// Pool is a validated pool name.
type Pool struct{ s string }

// Dataset is a validated filesystem or volume name, including its pool.
type Dataset struct{ s string }

// Snapshot is a validated snapshot name: dataset@snapshot.
type Snapshot struct{ s string }

// Bookmark is a validated bookmark name: dataset#bookmark.
type Bookmark struct{ s string }

func (p Pool) String() string     { return p.s }
func (d Dataset) String() string  { return d.s }
func (s Snapshot) String() string { return s.s }
func (b Bookmark) String() string { return b.s }

// Pool returns the pool the dataset belongs to.
func (d Dataset) Pool() Pool {
	p, _, _ := strings.Cut(d.s, "/")
	return Pool{p}
}

// Dataset returns the dataset part of a snapshot name.
func (s Snapshot) Dataset() Dataset {
	d, _, _ := strings.Cut(s.s, "@")
	return Dataset{d}
}

// Short returns the part after "@".
func (s Snapshot) Short() string {
	_, short, _ := strings.Cut(s.s, "@")
	return short
}

// Dataset returns the dataset part of a bookmark name.
func (b Bookmark) Dataset() Dataset {
	d, _, _ := strings.Cut(b.s, "#")
	return Dataset{d}
}

// Errors returned by the parse functions; each is wrapped with the name.
var (
	ErrEmpty        = errors.New("empty name")
	ErrTooLong      = fmt.Errorf("longer than %d characters", MaxLen)
	ErrCharacter    = errors.New("invalid character")
	ErrPoolStart    = errors.New("pool names must begin with a letter")
	ErrPoolReserved = errors.New("reserved pool name")
	ErrComponent    = errors.New("empty component, '.' or '..'")
	ErrDelimiter    = errors.New("missing or misplaced '@' or '#'")
)

// reservedPools may not be used as pool names, and names may not begin with
// reservedPoolPrefixes (zpool-create(8)).
var (
	reservedPools        = []string{"mirror", "raidz", "draid", "spare", "log"}
	reservedPoolPrefixes = []string{"mirror", "raidz", "draid", "spare"}
)

// ParsePool validates a pool name.
func ParsePool(s string) (Pool, error) {
	if err := checkLength(s); err != nil {
		return Pool{}, err
	}
	if !isLetter(s[0]) {
		return Pool{}, fmt.Errorf("%q: %w", s, ErrPoolStart)
	}
	if err := checkComponent(s); err != nil {
		return Pool{}, err
	}
	for _, r := range reservedPools {
		if s == r {
			return Pool{}, fmt.Errorf("%q: %w", s, ErrPoolReserved)
		}
	}
	for _, p := range reservedPoolPrefixes {
		if strings.HasPrefix(s, p) {
			return Pool{}, fmt.Errorf("%q begins with %q: %w", s, p, ErrPoolReserved)
		}
	}
	return Pool{s}, nil
}

// ParseDataset validates a filesystem or volume name: pool[/component]...
func ParseDataset(s string) (Dataset, error) {
	if err := checkLength(s); err != nil {
		return Dataset{}, err
	}
	poolPart, rest, nested := strings.Cut(s, "/")
	if _, err := ParsePool(poolPart); err != nil {
		return Dataset{}, err
	}
	if nested {
		for _, c := range strings.Split(rest, "/") {
			if err := checkComponent(c); err != nil {
				return Dataset{}, fmt.Errorf("%q: %w", s, err)
			}
		}
	}
	return Dataset{s}, nil
}

// ParseSnapshot validates dataset@snapshot.
func ParseSnapshot(s string) (Snapshot, error) {
	if err := parseSuffixed(s, '@'); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{s}, nil
}

// ParseBookmark validates dataset#bookmark.
func ParseBookmark(s string) (Bookmark, error) {
	if err := parseSuffixed(s, '#'); err != nil {
		return Bookmark{}, err
	}
	return Bookmark{s}, nil
}

func parseSuffixed(s string, delim byte) error {
	if err := checkLength(s); err != nil {
		return err
	}
	i := strings.IndexByte(s, delim)
	if i < 0 {
		return fmt.Errorf("%q: %w", s, ErrDelimiter)
	}
	if _, err := ParseDataset(s[:i]); err != nil {
		return err
	}
	if err := checkComponent(s[i+1:]); err != nil {
		return fmt.Errorf("%q: %w", s, err)
	}
	return nil
}

func checkLength(s string) error {
	switch {
	case s == "":
		return ErrEmpty
	case len(s) > MaxLen:
		return fmt.Errorf("%d characters: %w", len(s), ErrTooLong)
	}
	return nil
}

// checkComponent applies the per-component rules shared by pool names,
// dataset components, and snapshot and bookmark parts.
func checkComponent(c string) error {
	if c == "" || c == "." || c == ".." {
		return ErrComponent
	}
	for i := 0; i < len(c); i++ {
		if !componentByte(c[i]) {
			return fmt.Errorf("%w %q", ErrCharacter, c[i])
		}
	}
	return nil
}

func isLetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// componentByte reports whether b is allowed in a name component:
// letters, digits, and _ . : - and space (tested on 2.3.9).
func componentByte(b byte) bool {
	switch {
	case isLetter(b), b >= '0' && b <= '9':
		return true
	case b == '_', b == '.', b == ':', b == '-', b == ' ':
		return true
	}
	return false
}

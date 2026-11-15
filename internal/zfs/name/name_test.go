// SPDX-License-Identifier: GPL-3.0-or-later

package name

import (
	"errors"
	"strings"
	"testing"
)

// Cases marked "tested" were confirmed against OpenZFS 2.3.9 on throwaway
// pools; "man" cases come from zpool-create(8).

func TestParsePool(t *testing.T) {
	tests := []struct {
		in   string
		want error // nil means valid
	}{
		{"tank", nil},
		{"Pool", nil},              // tested
		{"fstuitest_a b", nil},     // tested: space allowed
		{"fstuitest_a:b.c-d", nil}, // tested
		{"pool-1", nil},            // tested
		{"logs", nil},              // only "log" itself is reserved
		{"", ErrEmpty},
		{"1pool", ErrPoolStart},       // man
		{"-pool", ErrPoolStart},       // would be read as an option
		{"_pool", ErrPoolStart},       // man
		{"tank@x", ErrCharacter},      // tested: real create rejects
		{"tank#x", ErrCharacter},      // tested
		{"tank\x01x", ErrCharacter},   // tested
		{"tank/x", ErrCharacter},      // tested
		{"caf\xc3\xa9", ErrCharacter}, // non-ASCII rejected for datasets (tested)
		{"mirror", ErrPoolReserved},   // man
		{"log", ErrPoolReserved},      // man
		{"raidz2pool", ErrPoolReserved},
		{"draid1x", ErrPoolReserved},
		{"spares", ErrPoolReserved},
		{strings.Repeat("a", MaxLen+1), ErrTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p, err := ParsePool(tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParsePool(%q) error = %v, want %v", tt.in, err, tt.want)
			}
			if err == nil && p.String() != tt.in {
				t.Fatalf("String() = %q, want %q", p, tt.in)
			}
		})
	}
}

func TestParseDataset(t *testing.T) {
	maxName := "tank/" + strings.Repeat("a", MaxLen-len("tank/"))
	tests := []struct {
		in   string
		want error
	}{
		{"tank", nil},
		{"tank/home", nil},
		{"tank/a b/c", nil},         // tested
		{"tank/-lead", nil},         // tested: components may start with "-"
		{"tank/-", nil},             // tested
		{"tank/.hidden", nil},       // tested
		{"tank/..x", nil},           // tested
		{"tank/x..", nil},           // tested
		{maxName, nil},              // tested: 255 accepted
		{maxName + "b", ErrTooLong}, // tested: 256 rejected
		{"tank/.", ErrComponent},    // tested
		{"tank/..", ErrComponent},   // tested
		{"tank//a", ErrComponent},   // tested
		{"tank/a/", ErrComponent},   // tested: trailing slash
		{"tank/x@", ErrCharacter},   // tested: '@' not allowed in a dataset
		{"tank/x#y", ErrCharacter},
		{"tank/x%y", ErrCharacter}, // tested: range syntax
		{"tank/x,y", ErrCharacter}, // tested: list syntax
		{"tank/\x1b[2J", ErrCharacter},
		{"1tank/a", ErrPoolStart},
		{"mirror/a", ErrPoolReserved},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, err := ParseDataset(tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseDataset(%q) error = %v, want %v", tt.in, err, tt.want)
			}
		})
	}
}

func TestParseSnapshot(t *testing.T) {
	tests := []struct {
		in   string
		want error
	}{
		{"tank/home@daily-2026-11-12", nil},
		{"tank/a b@-s.:_ x", nil}, // tested
		{"tank@-dashsnap", nil},   // tested
		{"tank/home", ErrDelimiter},
		{"tank/home@", ErrComponent}, // tested: empty snapshot part
		{"tank/home@a@b", ErrCharacter},
		{"tank/home@a%b", ErrCharacter},     // a range, not a name
		{"tank/home@sn\x1bp", ErrCharacter}, // tested
		{"tank//x@s", ErrComponent},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, err := ParseSnapshot(tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseSnapshot(%q) error = %v, want %v", tt.in, err, tt.want)
			}
		})
	}
}

func TestParseBookmark(t *testing.T) {
	if _, err := ParseBookmark("tank/home#-bm"); err != nil { // tested
		t.Fatalf("ParseBookmark: %v", err)
	}
	if _, err := ParseBookmark("tank/home@snap"); !errors.Is(err, ErrDelimiter) {
		t.Fatalf("ParseBookmark(snapshot name) error = %v, want ErrDelimiter", err)
	}
}

func TestNameParts(t *testing.T) {
	s, err := ParseSnapshot("tank/a b/c@daily")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Dataset().String(); got != "tank/a b/c" {
		t.Errorf("Dataset() = %q", got)
	}
	if got := s.Short(); got != "daily" {
		t.Errorf("Short() = %q", got)
	}
	if got := s.Dataset().Pool().String(); got != "tank" {
		t.Errorf("Pool() = %q", got)
	}
	b, err := ParseBookmark("tank/x#bm")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Dataset().String(); got != "tank/x" {
		t.Errorf("Bookmark Dataset() = %q", got)
	}
}

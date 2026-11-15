// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"errors"
	"strings"
	"testing"
)

func TestParseVersionTextMatchesJSON(t *testing.T) {
	text, err := ParseVersionText(golden(t, "zfs-version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	js, err := ParseVersion(golden(t, "zfs-version.json"))
	if err != nil {
		t.Fatal(err)
	}
	if text != js {
		t.Fatalf("plain output parsed to %+v, JSON to %+v", text, js)
	}
}

func TestParseVersionTextRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"zfs-2.3.9-1\n",
		"zfs-kmod-2.3.9-1\nzfs-2.3.9-1\n", // swapped
		"zfs-2.3.9-1\nzfs-kmod-2.3.9-1\nextra\n",
	} {
		if v, err := ParseVersionText([]byte(in)); err == nil {
			t.Errorf("%q parsed to %+v", in, v)
		}
	}
}

func TestSupported(t *testing.T) {
	for _, tc := range []struct {
		userland, kernel string
		ok               bool
	}{
		{"zfs-2.3.9-0+deb13u1", "zfs-kmod-2.3.9-0+deb13u1", true},
		{"zfs-2.3.0-1", "zfs-kmod-2.3.0-1", true},
		{"zfs-2.4.4-1", "zfs-kmod-2.4.4-1", true},
		{"zfs-2.2.11-r1", "zfs-kmod-2.2.11-r1", false},
		// Userland upgraded, old module still loaded: the module decides
		// what the ioctls return, so this is refused too.
		{"zfs-2.3.9-1", "zfs-kmod-2.2.7-1", false},
	} {
		v, err := newVersion(tc.userland, tc.kernel)
		if err != nil {
			t.Fatal(err)
		}
		err = v.supported()
		var old *TooOldError
		switch {
		case tc.ok && err != nil:
			t.Errorf("%s / %s refused: %v", tc.userland, tc.kernel, err)
		case !tc.ok && !errors.As(err, &old):
			t.Errorf("%s / %s: err = %v, want TooOldError", tc.userland, tc.kernel, err)
		case !tc.ok && !strings.Contains(err.Error(), tc.userland) || !tc.ok && !strings.Contains(err.Error(), tc.kernel):
			t.Errorf("message %q does not name both versions", err)
		}
	}
}

func TestTooOldErrorSanitises(t *testing.T) {
	v := Version{Userland: "zfs-2.2.0-\x1b[2J", Kernel: "zfs-kmod-2.2.0"}
	if msg := (&TooOldError{Version: v}).Error(); strings.Contains(msg, "\x1b") {
		t.Fatalf("message contains a raw escape: %q", msg)
	}
}

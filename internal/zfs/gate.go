// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/nuclear-gumbo/fstui/internal/execute"
	"github.com/nuclear-gumbo/fstui/internal/termtext"
)

// Minimum is the oldest OpenZFS release fstui supports, for userland and
// kernel module alike: it relies on the JSON output added in 2.3.
var Minimum = Release{2, 3, 0}

// ErrModuleNotLoaded is returned by DetectVersion when the zfs kernel
// module is not loaded.
var ErrModuleNotLoaded = errors.New("the zfs kernel module is not loaded; load it with: modprobe zfs")

// TooOldError is returned by DetectVersion for OpenZFS older than Minimum.
type TooOldError struct {
	Version Version
}

func (e *TooOldError) Error() string {
	return fmt.Sprintf("fstui needs OpenZFS %d.%d or newer; this system has %s (userland) and %s (kernel)",
		Minimum.Major, Minimum.Minor,
		termtext.New(e.Version.Userland).Display(), termtext.New(e.Version.Kernel).Display())
}

// DetectVersion checks that the zfs module is loaded and that userland and
// kernel module are both at least Minimum, and returns their versions.
// Releases before 2.3 reject "zfs version -j"; for those it reads the
// plain output so the refusal can name the version found.
func DetectVersion(ctx context.Context, run *execute.Runner) (Version, error) {
	return detectVersion(ctx, run, moduleDir)
}

func detectVersion(ctx context.Context, run *execute.Runner, module string) (Version, error) {
	if _, err := os.Stat(module); errors.Is(err, fs.ErrNotExist) {
		return Version{}, ErrModuleNotLoaded
	} else if err != nil {
		return Version{}, fmt.Errorf("checking for the zfs kernel module: %w", err)
	}

	res, jsonErr := run.Read(ctx, execute.Argv{Tool: execute.ZFS, Options: []string{"version", "-j"}})
	if jsonErr == nil {
		v, err := ParseVersion(res.Stdout)
		if err != nil {
			return Version{}, err
		}
		return v, v.supported()
	}
	var cmdErr *execute.CommandError
	if !errors.As(jsonErr, &cmdErr) {
		return Version{}, fmt.Errorf("reading the OpenZFS version: %w", jsonErr)
	}

	res, err := run.Read(ctx, execute.Argv{Tool: execute.ZFS, Options: []string{"version"}})
	if err != nil {
		return Version{}, fmt.Errorf("reading the OpenZFS version: %w (after: %w)", err, jsonErr)
	}
	v, err := ParseVersionText(res.Stdout)
	if err != nil {
		return Version{}, fmt.Errorf("%w (after: %w)", err, jsonErr)
	}
	if err := v.supported(); err != nil {
		return v, err
	}
	// New enough for -j, yet -j failed: that failure is the real problem.
	return Version{}, fmt.Errorf("reading the OpenZFS version: %w", jsonErr)
}

func (v Version) supported() error {
	if !v.UserlandRelease.AtLeast(Minimum) || !v.KernelRelease.AtLeast(Minimum) {
		return &TooOldError{Version: v}
	}
	return nil
}

// ParseVersionText parses the plain output of "zfs version": the userland
// version on the first line, the kernel module version on the second.
func ParseVersionText(data []byte) (Version, error) {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		return Version{}, fmt.Errorf("zfs version printed %d lines, want 2", len(lines))
	}
	return newVersion(lines[0], lines[1])
}

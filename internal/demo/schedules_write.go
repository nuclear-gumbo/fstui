// SPDX-License-Identifier: GPL-3.0-or-later

//go:build fstui_demo && !fstui_nowrite

package demo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/execute"
)

// Handles reports whether a step is a schedule change the demo carries
// out here rather than on its recorded pools.
func (s *Schedules) Handles(a execute.Argv) bool {
	switch a.Tool { //nolint:exhaustive // the rest are not schedule steps
	case execute.Systemctl, execute.RcUpdate, execute.RcService:
		return true
	case execute.Tee, execute.Chmod, execute.Mv:
		p := a.Operands[len(a.Operands)-1]
		return strings.HasPrefix(p, "/etc/systemd/") || strings.HasPrefix(p, "/etc/cron.d/") || strings.HasPrefix(p, "/etc/init.d/")
	}
	return false
}

// Apply carries out a schedule step inside the demo's system.
func (s *Schedules) Apply(a execute.Argv, stdin []byte) (execute.Result, error) {
	in := func(p string) string { return filepath.Join(s.Root, filepath.Clean("/"+p)) }
	var err error
	switch a.Tool { //nolint:exhaustive // Handles admits only these
	case execute.Systemctl:
		err = s.systemctl(a)
	case execute.Tee:
		err = os.WriteFile(in(a.Operands[0]), stdin, 0o600)
	case execute.Chmod:
		// the demo keeps no modes
	case execute.Mv:
		err = os.Rename(in(a.Operands[0]), in(a.Operands[1]))
	default:
		err = fmt.Errorf("%w: %s", errUnsupported, a)
	}
	if err != nil {
		return failed(a, err)
	}
	return execute.Result{Duration: time.Millisecond}, nil
}

// systemctl enables and disables units as systemd does: a link in the
// .wants directory of the target they are installed into.
func (s *Schedules) systemctl(a execute.Argv) error {
	if len(a.Options) == 0 { // daemon-reload
		return nil
	}
	unit := a.Operands[0]
	wants := "multi-user.target.wants"
	if strings.HasSuffix(unit, ".timer") {
		wants = "timers.target.wants"
	}
	link := filepath.Join(s.Root, "etc/systemd/system", wants, filepath.Base(unit))
	var err error
	switch a.Options[0] {
	case "enable":
		err = os.Symlink("/usr/lib/systemd/system/template", link)
	case "disable":
		err = os.Remove(link)
	}
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %w", a.Options[0], unit, err)
	}
	return nil // restart: nothing to show
}

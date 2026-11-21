// SPDX-License-Identifier: GPL-3.0-or-later

package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/nuclear-gumbo/fstui/internal/statedir"
)

// StateName is the state file, in the directory fstui check is given
// (check/ in the state directory).
const StateName = "state.json"

const stateVersion = 1

// State is what fstui check remembers between runs: which problems it
// has told which channel about and when, and the last kernel event it
// looked at. It holds keys, severities and times, never text from ZFS.
type State struct {
	Version int `json:"version"`
	// Boot is the kernel's boot ID: event IDs start again after a reboot.
	Boot      string            `json:"boot"`
	LastEvent uint64            `json:"last_event"`
	Problems  map[string]*Entry `json:"problems"`
}

// Entry is one problem that was open at the last run.
type Entry struct {
	Severity string `json:"severity"`
	State    string `json:"state"`
	// Sent is when each channel was last told, by channel name.
	Sent map[string]time.Time `json:"sent"`
	// ResolvedText is the line sent when the problem clears, and
	// Resolved the alert's choice of whether to send it (nil:
	// notifications.resolved): the alert's text is gone by then.
	ResolvedText string `json:"resolved_text,omitempty"`
	Resolved     *bool  `json:"resolved,omitempty"`
}

// LoadState reads the state file in dir; a missing file is an empty
// state, as on the first run.
func LoadState(dir string) (State, error) {
	s := State{Version: stateVersion, Problems: map[string]*Entry{}}
	if err := statedir.Check(dir); errors.Is(err, fs.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return s, fmt.Errorf("state: %w", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, StateName)) //nolint:gosec // in fstui's own private directory
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("state: %w", err)
	}
	var got State
	if err := json.Unmarshal(b, &got); err != nil || got.Version != stateVersion {
		return s, fmt.Errorf("state: %s is not a state file of this fstui; it is replaced on the next run", filepath.Join(dir, StateName))
	}
	if got.Problems == nil {
		got.Problems = map[string]*Entry{}
	}
	return got, nil
}

// Save writes the state to dir.
func (s State) Save(dir string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	if err := statedir.WriteFile(dir, StateName, b); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	return nil
}

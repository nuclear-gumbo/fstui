// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import (
	"testing"
	"time"
)

// A slow read spaces the next one out to busyFactor times its length;
// failures still back off from the interval.
func TestPollDelay(t *testing.T) {
	p := poller[int]{interval: 5 * time.Second}
	if d := p.delay(); d != 5*time.Second {
		t.Errorf("fresh: %v", d)
	}
	p.took = 35 * time.Second
	if d := p.delay(); d != 140*time.Second {
		t.Errorf("after a 35 s read: %v", d)
	}
	p.took, p.failures = 0, 2
	if d := p.delay(); d != 20*time.Second {
		t.Errorf("two failures: %v", d)
	}
}

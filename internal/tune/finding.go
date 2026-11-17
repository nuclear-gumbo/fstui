// SPDX-License-Identifier: GPL-3.0-or-later

package tune

import (
	"fmt"
	"strings"
)

// Verdict is a question's answer.
type Verdict int

// Verdicts, from no answer to the strongest.
const (
	NotEnough  Verdict = iota // too little was measured to say
	Fits                      // the setting fits what was measured
	WorthALook                // something may not fit; the figure says what
	Mismatch                  // the setting costs something measurable
)

// Word is the verdict as the tab shows it.
func (v Verdict) Word() string {
	return [...]string{"not enough data yet", "fits", "worth a look", "mismatch"}[v]
}

// Finding is the answer to one question: the verdict, the one figure that
// decides it, what it points at, and the evidence behind it.
type Finding struct {
	Question string
	Verdict  Verdict
	Figure   string   // the deciding figure, a short sentence
	Points   string   // the setting or hardware it points at, or what is missing
	Evidence []string // the lines enter shows
	Change   *Change  // the change the finding points to, if fstui can make it
}

// Change is a change a finding points to: a dataset property, a module
// parameter, or the ARC limits (Kind "property", "param", "arc").
type Change struct {
	Kind    string
	Dataset string // for a property
	Name    string // the property or parameter
	Value   string
}

// recordFor is the record size that fits requests averaging avg bytes:
// the power of two at or above it, from 4 KiB to 1 MiB.
func recordFor(avg uint64) uint64 {
	r := uint64(4 << 10)
	for r < avg && r < 1<<20 {
		r <<= 1
	}
	return r
}

// sizeValue is a size as zfs takes it in a property: 16K, 1M.
func sizeValue(n uint64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%dM", n>>20)
	}
	return fmt.Sprintf("%dK", n>>10)
}

// changeSep separates the parts of an encoded change; no dataset, property
// or parameter name contains it.
const changeSep = "\x1f"

// Encode writes c as one string, for a selection's names.
func (c Change) Encode() string {
	return strings.Join([]string{c.Kind, c.Dataset, c.Name, c.Value}, changeSep)
}

// DecodeChange reads a change Encode wrote.
func DecodeChange(s string) (Change, bool) {
	f := strings.Split(s, changeSep)
	if len(f) != 4 {
		return Change{}, false
	}
	return Change{Kind: f[0], Dataset: f[1], Name: f[2], Value: f[3]}, true
}

// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Kstat is a named kstat file under /proc/spl/kstat/zfs (arcstats,
// dmu_tx, zil, zfetchstats, a dataset's objset-0x<id>): a header line, a
// "name type data" line, then one statistic per line.
type Kstat struct {
	raw map[string]string
}

// maxKstatLine bounds one line; the longest real line, a dataset name in
// an objset file, is far shorter.
const maxKstatLine = 64 << 10

// ParseKstat parses a named kstat file. Lines that are not
// "name type value" (the header, a value with spaces) are skipped.
func ParseKstat(b []byte) (Kstat, error) {
	k := Kstat{raw: map[string]string{}}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), maxKstatLine)
	for line := 0; sc.Scan(); line++ {
		f := strings.Fields(sc.Text())
		if line < 2 || len(f) != 3 { // kstat header, column names
			continue
		}
		k.raw[f[0]] = f[2]
	}
	if err := sc.Err(); err != nil {
		return Kstat{}, fmt.Errorf("reading kstat: %w", err)
	}
	return k, nil
}

// Uint is the named counter; ok is false when it is missing or not an
// unsigned number.
func (k Kstat) Uint(name string) (n uint64, ok bool) {
	v, found := k.raw[name]
	if !found {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 64)
	return n, err == nil
}

// String is the named string statistic, such as an objset's dataset_name.
func (k Kstat) String(name string) string { return k.raw[name] }

// Txg is one row of /proc/spl/kstat/zfs/<pool>/txgs: a transaction group
// with its state and, once synced, how long each stage took (ns).
type Txg struct {
	TXG      uint64
	State    string // O open, Q quiescing, W waiting for sync, S syncing, C committed
	SyncTime uint64 // ns spent syncing
}

// ParseTxgs parses the txgs table: a header naming the columns, then one
// transaction group per line.
func ParseTxgs(b []byte) ([]Txg, error) {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil, nil
	}
	header := strings.Fields(lines[0])
	cols := map[string]int{}
	for i, c := range header {
		cols[c] = i
	}
	for _, c := range []string{"txg", "state", "stime"} {
		if _, ok := cols[c]; !ok {
			return nil, fmt.Errorf("txgs: no %s column", c)
		}
	}
	var out []Txg
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		// Against the header's width, not the column names: a name the
		// header repeats would leave cols smaller than a row must be.
		if len(f) < len(header) {
			continue // a row being written as it was read
		}
		num := func(c string) uint64 {
			n, _ := strconv.ParseUint(f[cols[c]], 10, 64)
			return n
		}
		out = append(out, Txg{TXG: num("txg"), State: f[cols["state"]], SyncTime: num("stime")})
	}
	return out, nil
}

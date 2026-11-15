// SPDX-License-Identifier: GPL-3.0-or-later

package zfs

import (
	"strings"
	"testing"
)

// Every property OpenZFS 2.3.9 reported in the captures has help, apart
// from user properties and the few zfsprops(7) does not describe.
func TestHelpCoversCapturedProperties(t *testing.T) {
	undocumented := map[string]bool{"mlslabel": true, "longname": true}
	ds, err := ParseDatasetGet(golden(t, "healthy", "zfs-get-p.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		for prop := range d.Properties {
			if strings.Contains(prop, ":") || undocumented[prop] {
				continue
			}
			if h, ok := DatasetHelp(prop); !ok || h.Summary == "" {
				t.Errorf("%s: no help for dataset property %s", d.Type, prop)
			}
		}
	}
	pools, err := ParsePoolGet(golden(t, "healthy", "zpool-get-p.json"))
	if err != nil {
		t.Fatal(err)
	}
	for prop := range pools[0].Properties {
		if h, ok := PoolHelp(prop); !ok || h.Summary == "" {
			t.Errorf("no help for pool property %s", prop)
		}
	}
}

// The new-data and creation-only statements are the ones zfsprops(7)
// makes; a property moved between them by mistake would mislead.
func TestHelpEffects(t *testing.T) {
	for prop, want := range map[string]Effect{
		"compression": NewDataOnly, "checksum": NewDataOnly, "copies": NewDataOnly, "recordsize": NewDataOnly,
		"encryption": CreationOnly, "casesensitivity": CreationOnly, "normalization": CreationOnly, "utf8only": CreationOnly,
		"used": Computed, "written": Computed, "mountpoint": Unspecified,
	} {
		if h, _ := DatasetHelp(prop); h.Effect != want {
			t.Errorf("%s: effect %v, want %v", prop, h.Effect, want)
		}
	}
	if h, _ := PoolHelp("feature@zpool_checkpoint"); !strings.Contains(h.Values, "active") {
		t.Errorf("feature help = %+v", h)
	}
}

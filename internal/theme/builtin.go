// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"embed"
	"path"
	"slices"
	"strings"
)

// DefaultName is the theme built from the Default table.
const DefaultName = "default"

//go:embed themes/*.yaml
var builtin embed.FS

// Builtin returns the YAML of a theme shipped with fstui.
func Builtin(name string) ([]byte, bool) {
	b, err := builtin.ReadFile(path.Join("themes", name+".yaml"))
	return b, err == nil
}

// BuiltinNames lists the shipped themes, default first.
func BuiltinNames() []string {
	entries, _ := builtin.ReadDir("themes") // embedded: cannot fail
	names := []string{DefaultName}
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	slices.Sort(names[1:])
	return names
}

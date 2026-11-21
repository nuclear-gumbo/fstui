// SPDX-License-Identifier: GPL-3.0-or-later

package zfsview

import "github.com/nuclear-gumbo/fstui/internal/config"

// settingsOr is the configuration the views share, or the defaults when
// none is given (tests). The views keep this one value and update it when
// the Config view saves, so settings apply without a restart.
func settingsOr(s *config.Config) *config.Config {
	if s != nil {
		return s
	}
	d := config.Default()
	return &d
}

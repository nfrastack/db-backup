// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package license

import "github.com/nfrastack/db-backup/internal/log"

func Notice(feature string, c FeatureState) {
	if c.Reason == "" {
		c.Reason = "no license installed"
	}
	log.Warn("license", "feature is not available in the Community edition", "feature", feature, "reason", c.Reason)
}

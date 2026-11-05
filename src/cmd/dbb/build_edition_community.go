// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build community

package main

import (
	"github.com/nfrastack/db-backup/internal/log"
)

const buildEdition = "community"

func anyCommandSupported() bool { return false }

func commandSupported(c command) bool {
	return false
}

func editionLabel() string {
	return "Community (AGPL)"
}
func editionLine() string {
	return "License: AGPL-3.0-or-later"
}

func licenseLabel() string {
	return "AGPL-3.0-or-later"
}

func maybePrintLicenseWarning(warn bool) {}

func editionLicenseID() string { return "" }

func reportLicenseVerdict(revoked bool) {}

func runtimeMode() string { return "community" }

func runtimeNote() string { return "" }

func startEditionBackgroundServices() {}

func logSupporterNudge(numJobs int) {
	if numJobs > 3 {
		log.Warn("license", "consider the Supported edition for advanced features and support: https://nfrastack.com/db-backup",
			"jobs", numJobs)
	}
}

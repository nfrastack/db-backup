// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !community

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/stats"
	revlicense "github.com/nfrastack/db-backup/supported/license"
)

const buildEdition = "supported"

func anyCommandSupported() bool {
	for _, c := range commands {
		if c.supported {
			return true
		}
	}
	return false
}

func cmdLicense(args []string) int {
	return revlicense.RunLicense(args, globalConfigPaths, stats.SharedKey())
}

func commandSupported(c command) bool {
	return c.supported
}

func editionLabel() string {
	return license.Edition()
}

func editionLicense() string {
	return license.Edition()
}

func editionLicenseID() string {
	return revlicense.InstalledID()
}

func editionLine() string {
	if license.Community() {
		line := "edition: community | License: AGPL-3.0-or-later"
		if lic, err := license.Installed(); err == nil && lic != nil {
			if rl := revlicense.RevokedEditionLine(lic); rl != "" {
				line = rl
			}
		}
		return line
	}
	lic, err := license.Installed()
	if err != nil || lic == nil {
		return "edition: community | License: AGPL-3.0-or-later"
	}
	expires := lic.ExpiresAt
	if t, err := time.Parse(time.RFC3339, expires); err == nil {
		expires = t.Format("2006-01-02")
	}
	tier := strings.TrimSpace(lic.Tier)
	if tier != "" && tier != "supported" {
		return fmt.Sprintf("edition: supported · %s | expires %s", tier, expires)
	}
	return fmt.Sprintf("edition: supported | expires %s", expires)
}

func licenseLabel() string {
	if license.Community() {
		return "AGPL-3.0-or-later"
	}
	lic, err := license.Installed()
	if err != nil || lic == nil {
		return "AGPL-3.0-or-later"
	}
	label := "NSLv1"
	if t, ok := lic.ExpiryTime(); ok {
		label += fmt.Sprintf(" (expires %s)", t.Format("2006-01-02"))
	}
	return label
}

func logSupporterNudge(numJobs int) {}

func runtimeMode() string {
	if license.Community() {
		return "community"
	}
	return "supporter"
}

func runtimeNote() string {
	lic, err := license.Installed()
	if err != nil || lic == nil {
		return "no license installed - running in community mode"
	}
	if note := revlicense.RevokedRuntimeNote(lic); note != "" {
		return note
	}
	if license.IsExpired(lic) {
		return "license expired - running in community mode"
	}
	return ""
}

func setupLicenseWatch() {
	revlicense.SetupLicenseWatch(globalConfigPaths)
}

func reportLicenseVerdict(revoked bool) {
	if id := revlicense.InstalledID(); id != "" && stats.OnLicenseVerdict != nil {
		stats.OnLicenseVerdict(id, revoked)
	}
}

func startEditionBackgroundServices() {
	stats.OnLicenseVerdict = revlicense.NoteServerVerdict
	license.IsRevoked = revlicense.IsRevokedCached
}

func maybePrintLicenseWarning(warn bool) {
	if warn {
		revlicense.PrintRevocationWarning()
	}
}

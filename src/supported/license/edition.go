// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package license

import (
	"fmt"

	"github.com/nfrastack/db-backup/internal/license"
)

func PrintRevocationWarning() {
	fmt.Println("WARNING: This license has been revoked - contact support (support@nfrastack.com).")
}

func RevokedEditionLine(lic *license.License) string {
	if lic != nil && lic.LicenseID != "" && IsRevokedCached(lic.LicenseID) {
		return "edition: community | license revoked - supporter features disabled"
	}
	return ""
}

func RevokedRuntimeNote(lic *license.License) string {
	if lic != nil && lic.LicenseID != "" && IsRevokedCached(lic.LicenseID) {
		return "license revoked - running in community mode"
	}
	return ""
}

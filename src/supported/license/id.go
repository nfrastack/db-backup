// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package license

import (
	"github.com/nfrastack/db-backup/internal/license"
)

func InstalledID() string {
	lic, err := license.Installed()
	if err != nil || lic == nil {
		return ""
	}
	return lic.LicenseID
}

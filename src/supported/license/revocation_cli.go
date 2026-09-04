// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package license

import (
	"context"
	"fmt"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/log"
)

func NoteServerVerdict(licenseID string, revoked bool) {
	if licenseID == "" {
		return
	}
	if err := MarkRevoked(licenseID, revoked); err != nil {
		log.Debug("license", "could not persist revocation verdict", "error", err.Error())
		return
	}
	if !revoked {
		log.Debug("license", "server reports license valid - revocation state cleared", "license_id", licenseID)
		return
	}
	log.Warn("license", "the server reports this license has been revoked - supporter features are now disabled. contact nfrastack for renewal.",
		"license_id", licenseID)
}

func PrintRevocationStatus(lic *license.License, configPaths []string, key string) {
	serverURL := ""
	if cfg, err := config.LoadStartupConfig(configPaths...); err == nil && cfg != nil && cfg.Stats != nil {
		serverURL = cfg.Stats.ServerURL
	}
	status, err := CheckRevocation(context.Background(), lic.LicenseID, serverURL, key)
	switch {
	case status != nil && status.Revoked:
		_ = MarkRevoked(lic.LicenseID, true)
		fmt.Println("  status  : REVOKED - supporter features are disabled")
	case err != nil:
	default:
		_ = MarkRevoked(lic.LicenseID, false)
	}
}

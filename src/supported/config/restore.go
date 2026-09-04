// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package configsupporter

import (
	"fmt"
	"os"

	"github.com/nfrastack/db-backup/internal/config"
)

func init() {
	config.SupporterResolveRestore = resolveRestore
}

func mergeRestore(dst, src *config.RestoreConfig) {
	if src == nil {
		return
	}
	if dst.File == "" {
		dst.File = src.File
	}
	if dst.Base == "" {
		dst.Base = src.Base
	}
	if dst.Connection == "" {
		dst.Connection = src.Connection
	}
	if dst.StorageRef == "" {
		dst.StorageRef = src.StorageRef
	}
	if dst.Identity == "" {
		dst.Identity = src.Identity
	}
	if dst.Passphrase == "" {
		dst.Passphrase = src.Passphrase
	}
}

func resolveRestore(c *config.Config, r *config.RestoreConfig) {
	if r == nil {
		return
	}
	if r.ProfileRef != "" && c != nil && c.RestoreProfiles != nil {
		if prof, ok := c.RestoreProfiles[r.ProfileRef]; ok {
			mergeRestore(r, &prof)
		} else {
			fmt.Fprintf(os.Stderr, "WARN: restore profile %q not found\n", r.ProfileRef)
		}
	}
	if r.Connection != "" && c != nil && c.Connections != nil {
		if conn, ok := c.Connections[r.Connection]; ok {
			r.Type = conn.Type
			r.Host = conn.Host
			r.Port = conn.Port
			r.User = conn.User
			r.Pass = conn.Pass
			r.AuthSource = conn.AuthSource
			if conn.TLS != nil {
				cp := *conn.TLS
				r.TLS = &cp
			}
		}
	}
	if r.StorageRef != "" && c != nil && c.StorageProfiles != nil {
		if prof, ok := c.StorageProfiles[r.StorageRef]; ok {
			cp := prof
			r.Storage = &cp
		}
	}
	r.Pass = config.ResolveSecret(r.Pass)
	r.Identity = config.ResolveSecret(r.Identity)
	r.Passphrase = config.ResolveSecret(r.Passphrase)
}

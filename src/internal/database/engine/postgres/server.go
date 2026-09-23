// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"strings"

	"github.com/nfrastack/db-backup/internal/database/common"
)

func ParseServerVersion(raw string) common.ServerVersion {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "PostgreSQL"))
	return common.ServerVersion{
		Engine:  "postgres",
		Version: common.NumericPrefix(rest),
		Arch:    common.ArchFromRaw(raw),
		Raw:     raw,
	}
}

func (d *Dumper) ServerVersion() common.ServerVersion {
	return d.server
}

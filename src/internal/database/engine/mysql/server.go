// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"strings"

	"github.com/nfrastack/db-backup/internal/database/common"
)

func ParseServerVersion(raw string) common.ServerVersion {
	engine := "mysql"
	if strings.Contains(strings.ToLower(raw), "mariadb") {
		engine = "mariadb"
	}
	return common.ServerVersion{
		Engine:  engine,
		Version: common.NumericPrefix(raw),
		Raw:     raw,
	}
}

func (d *Dumper) ServerVersion() common.ServerVersion {
	return d.server
}

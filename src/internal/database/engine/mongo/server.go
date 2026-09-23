// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import (
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ParseServerVersion(raw string) common.ServerVersion {
	return common.ServerVersion{
		Engine:  "mongo",
		Version: common.NumericPrefix(raw),
		Raw:     raw,
	}
}

func (d *Dumper) ServerVersion() common.ServerVersion {
	return d.server
}

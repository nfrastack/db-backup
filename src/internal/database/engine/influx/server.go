// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import (
	"strings"

	"github.com/nfrastack/db-backup/internal/database/common"
)

func ParseServerVersion(raw string) common.ServerVersion {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "v")
	trimmed = strings.TrimPrefix(trimmed, "V")
	return common.ServerVersion{
		Engine:  "influx",
		Version: common.NumericPrefix(trimmed),
		Raw:     raw,
	}
}

func (d *Dumper) ServerVersion() common.ServerVersion {
	return d.server
}

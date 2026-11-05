// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import (
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ParseServerVersion(raw, goarch string) common.ServerVersion {
	return common.ServerVersion{
		Engine:  "sqlite",
		Version: common.NumericPrefix(raw),
		Arch:    common.NormalizeArch(goarch),
		Raw:     raw,
	}
}

func (d *Dumper) ServerVersion() common.ServerVersion {
	return d.server
}

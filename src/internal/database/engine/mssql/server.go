// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import (
	"regexp"
	"strings"

	"github.com/nfrastack/db-backup/internal/database/common"
)

var mssqlYearRe = regexp.MustCompile(`(?i)SQL Server (\d+)`)

func ParseServerVersion(raw string) common.ServerVersion {
	first, _, _ := strings.Cut(raw, "\n")
	version := ""
	if m := mssqlYearRe.FindStringSubmatch(first); m != nil {
		version = m[1]
	} else {
		version = common.NumericPrefix(strings.TrimSpace(first))
	}
	return common.ServerVersion{
		Engine:  "mssql",
		Version: version,
		Arch:    common.ArchFromRaw(raw),
		Raw:     raw,
	}
}

func (d *Dumper) ServerVersion() common.ServerVersion {
	return d.server
}

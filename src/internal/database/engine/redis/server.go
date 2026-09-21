// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import (
	"strings"

	"github.com/nfrastack/db-backup/internal/database/common"
)

func ParseServerVersion(info string) common.ServerVersion {
	sv := common.ServerVersion{Engine: "redis", Raw: info}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if v, ok := strings.CutPrefix(line, "redis_version:"); ok {
			sv.Version = common.NumericPrefix(v)
		}
	}
	return sv
}

func (d *Dumper) ServerVersion() common.ServerVersion {
	return d.server
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build engine_redis

package database

import "github.com/nfrastack/db-backup/internal/database/engine/redis"

func init() {
	registerEngine(redis.Spec())
}

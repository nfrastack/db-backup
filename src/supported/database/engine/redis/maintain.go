// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package redis

import (
	"context"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	rediscore "github.com/nfrastack/db-backup/internal/database/engine/redis"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func init() {
	registry.RegisterMaintain(registry.MaintainSpec{Engine: "redis", Run: maintain})
}

func maintain(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	rdb := rediscore.ClientFor(host, port, pass, tlsCfg)
	defer rdb.Close()
	ctx := context.Background()

	var results []common.OpResult

	if common.Enabled("memory_purge", cfg) {
		r, start := common.StartOp("MEMORY PURGE")
		if err := rdb.Do(ctx, "MEMORY", "PURGE").Err(); err != nil {
			r.Status = "ERROR"
			r.Detail = err.Error()
		} else {
			r.Detail = "completed"
		}
		results = append(results, common.FinishOp(&r, start))
	}

	return results, nil
}

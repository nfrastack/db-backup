// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	pgcore "github.com/nfrastack/db-backup/internal/database/engine/postgres"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func init() {
	registry.RegisterMaintain(registry.MaintainSpec{Engine: "postgres", Run: maintain})
}

func maintain(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "postgres"
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgcore.ConnStr(user, pass, host, port, firstDB, tlsCfg))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var results []common.OpResult

	if common.Enabled("vacuum", cfg) {
		r, start := common.StartOp("VACUUM")
		if _, err := conn.Exec(ctx, "VACUUM"); err != nil {
			r.Status = "ERROR"
			r.Detail = err.Error()
		} else {
			r.Detail = "completed"
		}
		results = append(results, common.FinishOp(&r, start))
	}

	if common.Enabled("analyze", cfg) {
		r, start := common.StartOp("ANALYZE")
		if _, err := conn.Exec(ctx, "ANALYZE"); err != nil {
			r.Status = "ERROR"
			r.Detail = err.Error()
		} else {
			r.Detail = "completed"
		}
		results = append(results, common.FinishOp(&r, start))
	}

	if common.Enabled("reindex", cfg) {
		r, start := common.StartOp("REINDEX")
		if _, err := conn.Exec(ctx, "REINDEX DATABASE CONCURRENTLY "+pgx.Identifier{firstDB}.Sanitize()); err != nil {
			r.Status = "ERROR"
			r.Detail = err.Error()
		} else {
			r.Detail = "completed"
		}
		results = append(results, common.FinishOp(&r, start))
	}

	return results, nil
}

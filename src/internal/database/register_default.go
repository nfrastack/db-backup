// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !engine_couch && !engine_influx && !engine_mongo && !engine_mssql && !engine_mysql && !engine_postgres && !engine_redis && !engine_sqlite

package database

import (
	"github.com/nfrastack/db-backup/internal/database/engine/couch"
	"github.com/nfrastack/db-backup/internal/database/engine/influx"
	"github.com/nfrastack/db-backup/internal/database/engine/mongo"
	"github.com/nfrastack/db-backup/internal/database/engine/mssql"
	"github.com/nfrastack/db-backup/internal/database/engine/mysql"
	"github.com/nfrastack/db-backup/internal/database/engine/postgres"
	"github.com/nfrastack/db-backup/internal/database/engine/redis"
	"github.com/nfrastack/db-backup/internal/database/engine/sqlite"
)

func init() {
	registerEngine(couch.Spec())
	registerEngine(influx.Spec())
	registerEngine(mongo.Spec())
	registerEngine(mssql.Spec())
	registerEngine(mysql.Spec())
	registerEngine(postgres.Spec())
	registerEngine(redis.Spec())
	registerEngine(sqlite.Spec())
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build engine_mysql

// "mariadb" is also served by this engine

package database

import "github.com/nfrastack/db-backup/internal/database/engine/mysql"

func init() {
	registerEngine(mysql.Spec())
}

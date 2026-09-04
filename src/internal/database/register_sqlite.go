// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build engine_sqlite

package database

import "github.com/nfrastack/db-backup/internal/database/engine/sqlite"

func init() {
	registerEngine(sqlite.Spec())
}

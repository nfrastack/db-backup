// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build engine_mongo

package database

import "github.com/nfrastack/db-backup/internal/database/engine/mongo"

func init() {
	registerEngine(mongo.Spec())
}

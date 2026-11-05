// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import "github.com/nfrastack/db-backup/internal/database/registry"

func registerEngine(spec registry.EngineSpec) {
	registry.RegisterEngine(spec)
}

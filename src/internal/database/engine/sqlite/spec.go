// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import (
	"fmt"
	"io"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func databasePath(o registry.Options) string {
	if o.DB != "" {
		return o.DB
	}
	return o.Host
}

func Spec() registry.EngineSpec {
	return registry.EngineSpec{
		Name:        "sqlite",
		Aliases:     []string{"sqlite3"},
		Label:       "SQLite",
		DefaultPort: 0,
		New: func(o registry.Options) (registry.Engine, error) {
			p := databasePath(o)
			if p == "" {
				return nil, fmt.Errorf("sqlite requires the database file path (set --name <file> or --host <file>)")
			}
			return NewDumper(p), nil
		},
		Maintain: func(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
			return Maintain(dbName, cfg)
		},
		Restore: func(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			p := databasePath(registry.Options{Host: host, DB: dbName})
			if p == "" {
				return fmt.Errorf("sqlite restore requires the database file path (set --name <file> or --host <file>)")
			}
			return Restore(r, p)
		},
	}
}

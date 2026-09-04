// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"io"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func Spec() registry.EngineSpec {
	return registry.EngineSpec{
		Name:        "postgres",
		Aliases:     []string{"pgsql", "postgresql"},
		Label:       "PostgreSQL",
		DefaultPort: 5432,
		New: func(o registry.Options) (registry.Engine, error) {
			fallback := "postgres"
			if o.AuthSource != "" {
				fallback = o.AuthSource
			}
			return NewDumper(o.Host, o.Port, o.User, o.Pass, common.ConnDB(o.DB, fallback), o.TLS), nil
		},
		ListDatabases: func(host string, port int, user, pass, authSource string, tlsCfg *config.TLSConfig) ([]string, error) {
			return ListDatabases(host, port, user, pass, tlsCfg)
		},
		Maintain: func(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
			return Maintain(host, port, user, pass, dbName, cfg, tlsCfg)
		},
		Restore: func(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return Restore(r, host, port, user, pass, dbName, tlsCfg)
		},
	}
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import (
	"io"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func Spec() registry.EngineSpec {
	return registry.EngineSpec{
		Name:        "redis",
		Label:       "Redis",
		DefaultPort: 6379,
		New: func(o registry.Options) (registry.Engine, error) {
			db, err := ParseDBIndex(o.DB)
			if err != nil {
				return nil, err
			}
			d := NewDumper(o.Host, o.Port, o.Pass, o.TLS)
			d.db = db
			return d, nil
		},
		Maintain: func(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
			return Maintain(host, port, pass, cfg)
		},
		Restore: func(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return Restore(r, host, port, pass, dbName, tlsCfg)
		},
	}
}

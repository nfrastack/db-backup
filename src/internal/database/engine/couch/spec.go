// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package couch

import (
	"io"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func Spec() registry.EngineSpec {
	return registry.EngineSpec{
		Name:        "couch",
		Aliases:     []string{"couchdb"},
		Label:       "CouchDB",
		DefaultPort: 5984,
		New: func(o registry.Options) (registry.Engine, error) {
			return NewDumper(o.Host, o.Port, o.User, o.Pass, o.TLS), nil
		},
		ListDatabases: func(host string, port int, user, pass, authSource string, tlsCfg *config.TLSConfig) ([]string, error) {
			return ListDatabases(host, port, user, pass, tlsCfg)
		},
		Maintain: func(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
			return Maintain()
		},
		Restore: func(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return Restore(r, host, port, user, pass, dbName, authSource, tlsCfg)
		},
	}
}

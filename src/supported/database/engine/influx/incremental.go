// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package influx

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/engine/influx"
	"github.com/nfrastack/db-backup/internal/database/registry"
	"github.com/nfrastack/db-backup/internal/log"
)

func init() {
	registry.RegisterIncremental(registry.IncrementalSpec{
		Engine:       "influx",
		CheckSupport: CheckSupport,
		GetPosition:  GetPosition,
		Dump:         IncrementalDump,
	})
}

func CheckSupport(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
	d := influx.NewDumper(host, port, user, pass, dbName, 0, tlsCfg)
	if err := d.OpenContext(ctx); err != nil {
		return err
	}
	defer d.Close()
	if d.Version() == 2 {
		ok, err := d.ServerAtLeast21()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("influx: incremental physical backup requires server 2.1+")
		}
	}
	return nil
}

func GetPosition(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
	return time.Now().UTC().Format(time.RFC3339Nano), nil
}

func IncrementalDump(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error {
	label := "incremental"
	if strategy == "differential" {
		label = "differential"
	}
	d := influx.NewDumper(host, port, user, pass, dbName, 0, tlsCfg)
	d.SetConnectivity(&config.ConnectivityConfig{
		Enabled:       true,
		Method:        config.MethodFull,
		RetryInterval: 2,
		Timeout:       30,
	})
	if err := d.OpenContext(ctx); err != nil {
		return err
	}
	defer d.Close()

	end := time.Now().UTC().Format(time.RFC3339Nano)
	dbs := splitNames(dbName)
	log.Debug("influx", label+" backup start",
		"host", host, "port", port, "since", since, "end", end,
		"databases", strings.Join(dbs, ","))

	key := common.ProtocolKey(host, port, user, dbName)
	if d.Mode() == "physical" && d.Version() == 2 {
		if err := d.PhysicalBackupRange(w, dbs, since, end); err != nil {
			if influx.IsOperatorError(err) {
				log.Warn("influx", "physical slice forbidden - falling back to logical slice",
					"host", host, "error", err.Error())
			} else {
				return err
			}
		} else {
			common.RecordBackupProtocol(key, "physical")
			return nil
		}
	}
	if err := d.LogicalSlice(w, dbs, since, end); err != nil {
		return err
	}
	common.RecordBackupProtocol(key, "logical")
	return nil
}

func splitNames(dbName string) []string {
	var out []string
	for _, n := range strings.Split(dbName, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

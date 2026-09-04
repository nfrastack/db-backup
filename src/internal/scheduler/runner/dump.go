// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

func applyConnectivity(d connectivitySetter, cfg *config.ConnectivityConfig) {
	if d != nil {
		d.SetConnectivity(cfg)
	}
}

func dumpTo(ctx context.Context, w io.Writer, job config.JobConfig, port int, pass, dbName string, tableFilter *config.TableFilter, globalSchemaOnly bool, tlsCfg *config.TLSConfig, onTable func(db, table string)) error {
	dbType, host := job.Type, job.Host
	user := job.User

	effective := log.ParseLevel(job.LogLevel)
	if effective == 0 {
		effective = log.CurrentLevel()
	}
	if tr := tableTracer(job, effective, onTable); tr != nil {
		ctx = common.WithTracer(ctx, tr)
	}

	opts := database.Options{Type: dbType, Host: host, Port: port, User: user, Pass: pass, DB: dbName, Version: job.Version, TLS: tlsCfg, AuthSource: job.AuthSource}
	dumper, err := database.New(opts)
	if err != nil {
		return err
	}
	applyConnectivity(dumper, job.Connectivity)
	if tf, ok := dumper.(interface {
		SetTableFilter(*config.TableFilter, bool)
	}); ok {
		tf.SetTableFilter(tableFilter, globalSchemaOnly)
	}

	if strings.Contains(dbName, "__globals__") {
		if pg, ok := dumper.(interface{ DumpGlobals(io.Writer) error }); ok {
			if err := openWithContext(dumper, ctx); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer dumper.Close()
			if err := pg.DumpGlobals(w); err != nil {
				return fmt.Errorf("dump globals: %w", err)
			}
			return nil
		}
	}

	if err := openWithContext(dumper, ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer dumper.Close()
	if err := dumper.Dump(w, strings.Split(dbName, ",")); err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	return nil
}
func openWithContext(d database.Engine, ctx context.Context) error {
	type ctxOpener interface {
		OpenContext(context.Context) error
	}
	if c, ok := d.(ctxOpener); ok {
		return c.OpenContext(ctx)
	}
	return d.Open()
}

func tableTracer(job config.JobConfig, effective log.Level, onTable func(db, table string)) common.TableTracer {
	if effective > log.LevelDebug && onTable == nil {
		return nil
	}
	return func(db, table string) {
		if onTable != nil {
			onTable(db, table)
		}
		if effective <= log.LevelDebug {
			JLog(log.LevelDebug, job, "dumping table",
				"status", "debug", "step", "dump", "db", db, "table", table)
		}
	}
}

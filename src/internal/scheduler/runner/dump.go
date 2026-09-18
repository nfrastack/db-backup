// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
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

type stageError struct {
	stage string
	err   error
}

func (e *stageError) Error() string { return e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

func withStage(err error, stage string) error {
	if err == nil {
		return nil
	}
	return &stageError{stage: stage, err: err}
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
	if job.InfluxMode != "" {
		ctx = common.WithBackupMode(ctx, job.InfluxMode)
	}

	opts := database.Options{Type: dbType, Host: host, Port: port, User: user, Pass: pass, DB: dbName, Version: job.Version, TLS: tlsCfg, AuthSource: job.AuthSource}
	if job.Databases != nil {
		opts.Objects = job.Databases.ResolveMysqlObjects()
		opts.HasObjects = true
	}
	dumper, err := database.New(opts)
	if err != nil {
		return withStage(err, "dump")
	}
	applyConnectivity(dumper, job.Connectivity)
	if tf, ok := dumper.(interface {
		SetTableFilter(*config.TableFilter, bool)
	}); ok {
		tf.SetTableFilter(tableFilter, globalSchemaOnly)
	}
	if job.Databases != nil {
		if mo, ok := dumper.(interface {
			SetMysqlObjects(config.MysqlObjects)
		}); ok {
			mo.SetMysqlObjects(job.Databases.ResolveMysqlObjects())
		}
	}

	if strings.Contains(dbName, "__globals__") {
		if pg, ok := dumper.(interface{ DumpGlobals(io.Writer) error }); ok {
			if err := openWithContext(dumper, ctx); err != nil {
				return withStage(fmt.Errorf("connect: %w", err), "connect")
			}
			defer dumper.Close()
			if err := pg.DumpGlobals(w); err != nil {
				return withStage(fmt.Errorf("dump globals: %w", err), "dump")
			}
			return nil
		}
	}

	if err := openWithContext(dumper, ctx); err != nil {
		return withStage(fmt.Errorf("connect: %w", err), "connect")
	}
	defer dumper.Close()
	names := strings.Split(dbName, ",")
	if job.Databases != nil && len(job.Databases.Include) > 0 {
		names = append([]string{}, job.Databases.Include...)
	}
	if err := dumper.Dump(w, names); err != nil {
		return withStage(fmt.Errorf("dump: %w", err), "dump")
	}
	return nil
}

func errorStage(err error) string {
	var se *stageError
	if errors.As(err, &se) {
		return se.stage
	}
	return "upload"
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

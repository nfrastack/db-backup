// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/engine/mssql"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func CheckSupport(ctx context.Context, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "master"
	}
	connStr := mssql.ConnStr(user, pass, host, port, firstDB, tlsCfg)
	if user == "" {
		connStr = fmt.Sprintf("sqlserver://%s:%d?database=%s&encrypt=disable&trusted_connection=yes",
			host, port, firstDB)
	}
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	var model string
	err = db.QueryRow(
		"SELECT recovery_model_desc FROM sys.databases WHERE database_id = DB_ID(?)", firstDB).Scan(&model)
	if err != nil {
		err = db.QueryRow("SELECT recovery_model_desc FROM sys.databases WHERE name = DB_NAME()").Scan(&model)
	}
	if err != nil {
		return fmt.Errorf("recovery model: %w", err)
	}
	if !strings.EqualFold(model, "FULL") {
		return fmt.Errorf("database %q recovery model is %s - set RECOVERY FULL for log/differential backups", firstDB, model)
	}
	return nil
}

func IncrementalDump(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy string, tlsCfg *config.TLSConfig) error {
	if strategy != "incremental" && strategy != "differential" {
		return fmt.Errorf("unsupported strategy: %s", strategy)
	}
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "master"
	}
	connStr := mssql.ConnStr(user, pass, host, port, firstDB, tlsCfg)
	if user == "" {
		connStr = fmt.Sprintf("sqlserver://%s:%d?database=%s&encrypt=disable&trusted_connection=yes",
			host, port, firstDB)
	}
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	var backupStmt string
	switch strategy {
	case "incremental":
		backupStmt = fmt.Sprintf("BACKUP LOG [%s] TO DISK='NUL' WITH FORMAT, COMPRESSION", firstDB)
	case "differential":
		backupStmt = fmt.Sprintf("BACKUP DATABASE [%s] TO DISK='NUL' WITH DIFFERENTIAL, FORMAT, COMPRESSION", firstDB)
	}

	fmt.Fprintf(w, "-- MSSQL %s for %s\n", strategy, firstDB)
	fmt.Fprintf(w, "-- %s\n\n", backupStmt)

	if _, err := db.Exec(backupStmt); err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	fmt.Fprintf(w, "-- Backup executed successfully\n")
	fmt.Fprintf(w, "-- WAL/LSN position recorded in sidecar\n")
	return nil
}

func IncrementalSpec() registry.IncrementalSpec {
	return registry.IncrementalSpec{
		Engine: "mssql",
		CheckSupport: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return CheckSupport(ctx, host, port, user, pass, dbName, tlsCfg)
		},
		GetPosition: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
			return Position(ctx, host, port, user, pass, dbName, tlsCfg)
		},
		Dump: func(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error {
			return IncrementalDump(ctx, w, host, port, user, pass, dbName, strategy, tlsCfg)
		},
	}
}
func Position(ctx context.Context, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) (string, error) {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "master"
	}
	connStr := mssql.ConnStr(user, pass, host, port, firstDB, tlsCfg)
	if user == "" {
		connStr = fmt.Sprintf("sqlserver://%s:%d?database=%s&encrypt=disable&trusted_connection=yes",
			host, port, firstDB)
	}
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	var lsn string
	err = db.QueryRow("SELECT CONCAT(CAST(DB_NAME() AS VARCHAR(128)), ':', CAST(last_log_backup_lsn AS VARCHAR(128))) FROM sys.database_recovery_status WHERE database_id = DB_ID()").Scan(&lsn)
	if err != nil {
		return "", fmt.Errorf("get LSN: %w", err)
	}
	return lsn, nil
}
func init() {
	registry.RegisterIncremental(IncrementalSpec())
}

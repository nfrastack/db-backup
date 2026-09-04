// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ListDatabases(host string, port int, user, pass string, tlsCfg *config.TLSConfig) ([]string, error) {
	connStr := ConnStr(user, pass, host, port, "master", tlsCfg)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT name FROM sys.databases WHERE name NOT IN ('master', 'tempdb', 'model', 'msdb')")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	var dbs []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		dbs = append(dbs, name)
	}
	return dbs, rows.Err()
}

func Maintain(host string, port int, user, pass, dbName string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "master"
	}
	connStr := ConnStr(user, pass, host, port, firstDB, tlsCfg)
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	var results []common.OpResult

	if common.Enabled("check_tables", cfg) {
		r, start := common.StartOp("DBCC CHECKDB")
		if _, err := db.Exec(fmt.Sprintf("DBCC CHECKDB([%s]) WITH NO_INFOMSGS", firstDB)); err != nil {
			r.Status = "ERROR"
			r.Detail = err.Error()
		} else {
			r.Detail = "completed"
		}
		results = append(results, common.FinishOp(&r, start))
	}

	return results, nil
}

func Restore(r io.Reader, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "master"
	}
	connStr := ConnStr(user, pass, host, port, firstDB, tlsCfg)
	if user == "" {
		connStr = fmt.Sprintf("sqlserver://%s:%d?database=%s&encrypt=disable&trusted_connection=yes",
			host, port, firstDB)
	}
	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read dump: %w", err)
	}
	text := string(data)

	sourceDB := extractMSSQLSourceDB(text)
	if sourceDB != "" && sourceDB != firstDB {
		text = strings.ReplaceAll(text, "["+sourceDB+"].", "["+firstDB+"].")
	}

	for _, stmt := range strings.Split(text, "\nGO\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec: %w (%.80s)", err, stmt)
		}
	}
	return nil
}

func extractMSSQLSourceDB(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "-- Database:"); ok {
			name := strings.TrimSpace(rest)
			if name != "" {
				return name
			}
		}
	}
	return ""
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import (
	"bufio"
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

	return restoreStream(r, func(batch, sourceDB string) error {
		stmt := rewriteBatch(batch, sourceDB, firstDB)
		if strings.TrimSpace(stmt) == "" {
			return nil
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec: %w (%.80s)", err, stmt)
		}
		return nil
	})
}

func isGOSeparator(line string) bool {
	return strings.EqualFold(strings.TrimSpace(line), "GO")
}

func markerSourceDB(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(trimmed, "-- Database:")
	if !ok {
		return "", false
	}
	if name := strings.TrimSpace(rest); name != "" {
		return name, true
	}
	return "", false
}

func restoreStream(r io.Reader, yield func(batch, sourceDB string) error) error {
	var pending strings.Builder
	sourceDB := ""
	flush := func() error {
		batch := strings.TrimSpace(pending.String())
		pending.Reset()
		if batch == "" {
			return nil
		}
		return yield(batch, sourceDB)
	}

	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			noline := strings.TrimSuffix(line, "\n")
			if sourceDB == "" {
				if src, ok := markerSourceDB(noline); ok {
					sourceDB = src
				}
			}
			if isGOSeparator(noline) {
				if err := flush(); err != nil {
					return err
				}
			} else {
				pending.WriteString(noline)
				pending.WriteByte('\n')
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read dump: %w", err)
		}
	}
	return flush()
}

func rewriteBatch(batch, sourceDB, firstDB string) string {
	if sourceDB != "" && sourceDB != firstDB {
		batch = strings.ReplaceAll(batch, "["+sourceDB+"].", "["+firstDB+"].")
	}
	return batch
}

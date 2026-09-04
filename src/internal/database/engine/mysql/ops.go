// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ListDatabases(host string, port int, user, pass string, tlsCfg *config.TLSConfig) ([]string, error) {
	db, err := sql.Open("mysql", ConnectDSN(user, pass, host, port, "charset=utf8mb4", TLSNameFor(tlsCfg)))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	rows, err := db.Query("SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("show databases: %w", err)
	}
	defer rows.Close()

	var dbs []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		if !isSystemDB(name) {
			dbs = append(dbs, name)
		}
	}
	return dbs, rows.Err()
}

func Maintain(host string, port int, user, pass, dbName string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	db, err := sql.Open("mysql", ConnectDSN(user, pass, host, port, "charset=utf8mb4&multiStatements=true", TLSNameFor(tlsCfg)))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	var results []common.OpResult
	names := common.DBNamesList(dbName)

	for _, n := range names {
		if _, err := db.Exec("USE `" + n + "`"); err != nil {
			continue
		}

		if common.Enabled("check_tables", cfg) {
			if r := runMySQLOp(db, "CHECK TABLE", n); r != nil {
				results = append(results, *r)
			}
		}
	}
	return results, nil
}

func Restore(r io.Reader, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	db, err := sql.Open("mysql", ConnectDSN(user, pass, host, port,
		"charset=utf8mb4&multiStatements=true&interpolateParams=true", TLSNameFor(tlsCfg)))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	if firstDB != "" {
		if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS `" + firstDB + "`"); err != nil {
			return fmt.Errorf("create db: %w", err)
		}
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read dump: %w", err)
	}

	targets := strings.Split(firstDB, ",")
	if len(targets) == 1 && targets[0] != "" {
		reUSE := regexp.MustCompile("(?i)USE\\s+`[^`]+`\\s*;")
		data = reUSE.ReplaceAll(data, []byte("USE `"+targets[0]+"`;"))
		reInsert := regexp.MustCompile("(?i)INSERT\\s+INTO\\s+`[^`]+`\\.")
		data = reInsert.ReplaceAll(data, []byte("INSERT INTO `"+targets[0]+"`."))
		reOther := regexp.MustCompile("(?i)(FROM|UPDATE|DELETE\\s+FROM|TRUNCATE\\s+TABLE|RENAME\\s+TABLE|DROP\\s+TABLE|ALTER\\s+TABLE|CREATE\\s+TABLE)\\s+`[^`]+`\\.")
		data = reOther.ReplaceAll(data, []byte("$1 `"+targets[0]+"`."))
		if firstDB != "" {
			rePlain := regexp.MustCompile("(?i)(INSERT\\s+INTO|UPDATE|DELETE\\s+FROM|TRUNCATE\\s+TABLE|RENAME\\s+TABLE|DROP\\s+TABLE|ALTER\\s+TABLE|CREATE\\s+TABLE)\\s+([A-Za-z0-9_]+)\\.")
			data = rePlain.ReplaceAll(data, []byte("${1} "+firstDB+"."))
		}
	}

	stmts := strings.Split(string(data), ";\n")
	for _, raw := range stmts {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}
	return nil
}

func listMySQLTables(db *sql.DB, dbName string) ([]string, error) {
	rows, err := db.Query("SHOW TABLES FROM `" + dbName + "`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func runMySQLOp(db *sql.DB, op, dbName string) *common.OpResult {
	r, start := common.StartOp(op)
	tables, err := listMySQLTables(db, dbName)
	if err != nil {
		res := common.FinishOp(&r, start)
		res.Status = "ERROR"
		res.Detail = fmt.Sprintf("list tables: %v", err)
		return &res
	}
	var errs []string
	for _, t := range tables {
		if _, err := db.Exec(fmt.Sprintf("%s `%s`.`%s`", op, dbName, t)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t, err))
		}
	}
	status := "OK"
	detail := fmt.Sprintf("%d tables", len(tables))
	if len(errs) > 0 {
		status = "ERROR"
		detail = strings.Join(errs, "; ")
	}
	res := common.FinishOp(&r, start)
	res.Status = status
	res.Detail = detail
	return &res
}

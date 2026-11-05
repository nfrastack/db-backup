// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package mysql

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	mysqlcore "github.com/nfrastack/db-backup/internal/database/engine/mysql"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func init() {
	registry.RegisterMaintain(registry.MaintainSpec{Engine: "mysql", Run: maintain})
}

func listTables(db *sql.DB, dbName string) ([]string, error) {
	rows, err := db.Query("SHOW TABLES FROM " + quoteMySQLIdent(dbName))
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
func maintain(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	db, err := sql.Open("mysql", mysqlcore.ConnectDSN(user, pass, host, port, "charset=utf8mb4&multiStatements=true", mysqlcore.TLSNameFor(tlsCfg)))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	var results []common.OpResult
	for _, n := range common.DBNamesList(dbName) {
		if _, err := db.Exec("USE " + quoteMySQLIdent(n)); err != nil {
			continue
		}
		if common.Enabled("check_tables", cfg) {
			if r := runOp(db, "CHECK TABLE", n); r != nil {
				results = append(results, *r)
			}
		}
		if common.Enabled("optimize", cfg) {
			if r := runOp(db, "OPTIMIZE TABLE", n); r != nil {
				results = append(results, *r)
			}
		}
		if common.Enabled("analyze", cfg) {
			if r := runOp(db, "ANALYZE TABLE", n); r != nil {
				results = append(results, *r)
			}
		}
	}
	return results, nil
}
func quoteMySQLIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func runOp(db *sql.DB, op, dbName string) *common.OpResult {
	r, start := common.StartOp(op)
	tables, err := listTables(db, dbName)
	if err != nil {
		res := common.FinishOp(&r, start)
		res.Status = "ERROR"
		res.Detail = fmt.Sprintf("list tables: %v", err)
		return &res
	}
	var errs []string
	for _, t := range tables {
		if _, err := db.Exec(fmt.Sprintf("%s %s.%s", op, quoteMySQLIdent(dbName), quoteMySQLIdent(t))); err != nil {
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

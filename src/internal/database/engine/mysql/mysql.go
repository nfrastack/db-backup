// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

type Dumper struct {
	host      string
	port      int
	user      string
	pass      string
	db        *sql.DB
	isMariaDB bool
	serverVer string
	connCfg   *config.ConnectivityConfig
	tlsCfg    *config.TLSConfig
	tlsName   string
	ctx       context.Context

	SingleTransaction bool
	Events            bool
	Routines          bool
	SplitDB           bool
	Tables            *config.TableFilter
	SchemaOnly        bool
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (d *Dumper) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	var tx *sql.Tx
	var err error

	if d.SingleTransaction {
		tx, err = d.db.BeginTx(d.ctxOrBg(), nil)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()
	}

	conn := d.db

	d.writeHeader(w)
	d.writeVersion(w)

	if len(dbNames) == 1 && strings.ToLower(dbNames[0]) == "all" {
		var err error
		dbNames, err = d.listDatabases()
		if err != nil {
			return err
		}
	}

	for _, dbName := range dbNames {
		if err := d.dumpDatabase(w, conn, tx, dbName); err != nil {
			return fmt.Errorf("dump %s: %w", dbName, err)
		}
	}

	if d.SingleTransaction {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
	}

	d.writeFooter(w)
	return nil
}

func NewDumper(host string, port int, user, pass string, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 3306
	}
	d := &Dumper{
		host:              host,
		port:              port,
		user:              user,
		pass:              pass,
		SingleTransaction: true,
		Events:            true,
		Routines:          true,
		connCfg: &config.ConnectivityConfig{
			Enabled:       true,
			Method:        config.MethodFull,
			RetryInterval: 5,
			Timeout:       300,
		},
	}
	if len(tlsCfg) > 0 && tlsCfg[0] != nil {
		d.tlsCfg = tlsCfg[0]
	}
	return d
}

func (d *Dumper) Open() error {
	return d.OpenContext(context.Background())
}

func (d *Dumper) OpenContext(ctx context.Context) error {
	d.ctx = ctx
	probe := func() error { return common.TCPDial(d.host, d.port) }
	connect := func() error {
		if d.tlsName == "" && d.tlsCfg != nil {
			tc, err := common.BuildTLSConfig(d.tlsCfg)
			if err != nil {
				return err
			}
			if tc != nil {
				if name, err := RegisterTLS(tc); err == nil {
					d.tlsName = name
				}
			}
		}
		dsn := ConnectDSN(d.user, d.pass, d.host, d.port,
			"charset=utf8mb4&multiStatements=true&interpolateParams=true", d.tlsName)
		var err error
		d.db, err = sql.Open("mysql", dsn)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		return nil
	}
	ping := func() error {
		if err := d.db.PingContext(ctx); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		var ver string
		if err := d.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&ver); err != nil {
			return fmt.Errorf("ping version: %w", err)
		}
		d.serverVer = ver
		d.isMariaDB = strings.Contains(strings.ToLower(ver), "mariadb")
		return nil
	}
	return common.WithConnectivity(ctx, "mysql", d.connCfg, probe, connect, ping)
}

func (d *Dumper) SetConnectivity(cfg *config.ConnectivityConfig) {
	if cfg != nil {
		d.connCfg = cfg
	}
}

func (d *Dumper) SetTableFilter(f *config.TableFilter, schemaOnly bool) {
	d.Tables = f
	d.SchemaOnly = schemaOnly
}
func (d *Dumper) ctxOrBg() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

func (d *Dumper) dumpDatabase(w io.Writer, conn *sql.DB, tx *sql.Tx, dbName string) error {
	tables, err := d.listTables(conn, tx, dbName)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\n-- Database: %s\n", dbName)
	fmt.Fprintf(w, "USE %s;\n\n", quoteMySQLIdent(dbName))

	for _, table := range tables {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(table)
			if !included {
				continue
			}
		}
		common.TraceTable(d.ctxOrBg(), dbName, table)
		if err := d.dumpTable(w, conn, tx, dbName, table); err != nil {
			return err
		}
	}

	if d.Events {
		d.dumpEvents(w, conn, tx, dbName)
	}

	if d.Routines {
		d.dumpRoutines(w, conn, tx, dbName)
	}

	return nil
}

func (d *Dumper) dumpEvents(w io.Writer, conn *sql.DB, tx *sql.Tx, dbName string) {
	fmt.Fprintf(w, "\n-- Events for %s\n", dbName)
}

func (d *Dumper) dumpRoutines(w io.Writer, conn *sql.DB, tx *sql.Tx, dbName string) {
	fmt.Fprintf(w, "\n-- Stored routines for %s\n", dbName)
}

func (d *Dumper) dumpTable(w io.Writer, conn *sql.DB, tx *sql.Tx, dbName, table string) error {
	fmt.Fprintf(w, "\n-- Table: %s\n", table)

	ctx := d.ctxOrBg()
	var createSQL string
	q := "SHOW CREATE TABLE " + quoteMySQLIdent(dbName) + "." + quoteMySQLIdent(table)
	if tx != nil {
		if err := tx.QueryRowContext(ctx, q).Scan(&table, &createSQL); err != nil {
			return fmt.Errorf("show create %s: %w", table, err)
		}
	} else {
		if err := conn.QueryRowContext(ctx, q).Scan(&table, &createSQL); err != nil {
			return fmt.Errorf("show create %s: %w", table, err)
		}
	}
	fmt.Fprintf(w, "DROP TABLE IF EXISTS %s;\n", quoteMySQLIdent(table))
	fmt.Fprintf(w, "%s;\n\n", createSQL)

	schemaOnly := d.SchemaOnly
	if d.Tables != nil {
		_, so := d.Tables.Apply(table)
		schemaOnly = schemaOnly || so
	}
	if schemaOnly {
		return nil
	}

	var rowScanner func(io.Writer) error
	if tx != nil {
		rowScanner = func(w io.Writer) error {
			return d.streamRows(w, tx, dbName, table)
		}
	} else {
		rowScanner = func(w io.Writer) error {
			return d.streamRows(w, conn, dbName, table)
		}
	}

	if err := rowScanner(w); err != nil {
		return err
	}

	return nil
}

func escapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func isBlobType(t string) bool {
	switch strings.ToLower(t) {
	case "blob", "tinyblob", "mediumblob", "longblob", "binary", "varbinary", "geometry":
		return true
	}
	return false
}

func isSystemDB(name string) bool {
	switch name {
	case "information_schema", "performance_schema", "mysql", "sys":
		return true
	}
	return false
}

func (d *Dumper) listDatabases() ([]string, error) {
	var dbs []string
	rows, err := d.db.QueryContext(d.ctxOrBg(), "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, fmt.Errorf("scan database row: %w", err)
		}
		if !isSystemDB(db) {
			dbs = append(dbs, db)
		}
	}
	return dbs, rows.Err()
}

func (d *Dumper) listTables(conn *sql.DB, tx *sql.Tx, dbName string) ([]string, error) {
	var tables []string
	ctx := d.ctxOrBg()
	var rows *sql.Rows
	var err error
	q := "SHOW TABLES FROM " + quoteMySQLIdent(dbName)
	if tx != nil {
		rows, err = tx.QueryContext(ctx, q)
	} else {
		rows, err = conn.QueryContext(ctx, q)
	}
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("scan table row: %w", err)
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}
func quoteMySQLIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func (d *Dumper) streamRows(w io.Writer, q querier, dbName, table string) error {
	rows, err := q.QueryContext(d.ctxOrBg(), "SELECT * FROM "+quoteMySQLIdent(dbName)+"."+quoteMySQLIdent(table))
	if err != nil {
		return fmt.Errorf("select %s.%s: %w", dbName, table, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return err
	}

	var rowCount int
	for rows.Next() {
		if rowCount == 0 {
			qCols := make([]string, len(cols))
			for i, c := range cols {
				qCols[i] = quoteMySQLIdent(c)
			}
			fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES\n", quoteMySQLIdent(table), strings.Join(qCols, ", "))
		} else {
			fmt.Fprintf(w, ",\n")
		}

		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}

		fmt.Fprintf(w, "(")
		for i, val := range values {
			if i > 0 {
				fmt.Fprintf(w, ", ")
			}
			switch v := val.(type) {
			case nil:
				fmt.Fprintf(w, "NULL")
			case []byte:
				scanType := colTypes[i].DatabaseTypeName()
				if isBlobType(scanType) {
					fmt.Fprintf(w, "0x%x", v)
				} else {
					fmt.Fprintf(w, "'%s'", escapeString(string(v)))
				}
			case int64:
				fmt.Fprintf(w, "%d", v)
			case float64:
				fmt.Fprintf(w, "%v", v)
			case bool:
				if v {
					fmt.Fprintf(w, "1")
				} else {
					fmt.Fprintf(w, "0")
				}
			case string:
				fmt.Fprintf(w, "'%s'", escapeString(v))
			case fmt.Stringer:
				fmt.Fprintf(w, "'%s'", escapeString(v.String()))
			default:
				fmt.Fprintf(w, "'%s'", escapeString(fmt.Sprintf("%v", v)))
			}
		}
		fmt.Fprintf(w, ")")

		rowCount++
	}

	if rowCount > 0 {
		fmt.Fprintf(w, ";\n")
	}

	return rows.Err()
}

func (d *Dumper) writeFooter(w io.Writer) {
	fmt.Fprintf(w, "/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;\n")
	fmt.Fprintf(w, "/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;\n")
	fmt.Fprintf(w, "/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;\n")
	fmt.Fprintf(w, "/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;\n")
	fmt.Fprintf(w, "/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;\n")
	fmt.Fprintf(w, "/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;\n")
	fmt.Fprintf(w, "/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;\n")
	fmt.Fprintf(w, "/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;\n")
	fmt.Fprintf(w, "\n-- Dump completed\n")
}

func (d *Dumper) writeHeader(w io.Writer) {
	fmt.Fprintf(w, `-- dbbackup MySQL/MariaDB dump
-- Host: %s  Server: %s
--
`, d.host, d.serverVer)
	fmt.Fprintf(w, "/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;\n")
	fmt.Fprintf(w, "/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;\n")
	fmt.Fprintf(w, "/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;\n")
	fmt.Fprintf(w, " SET NAMES utf8mb4 ;\n")
	fmt.Fprintf(w, "/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;\n")
	fmt.Fprintf(w, "/*!40103 SET TIME_ZONE='+00:00' */;\n")
	fmt.Fprintf(w, "/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;\n")
	fmt.Fprintf(w, "/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;\n")
	fmt.Fprintf(w, "/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;\n")
	fmt.Fprintf(w, "/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;\n\n")
}

func (d *Dumper) writeVersion(w io.Writer) {
	fmt.Fprintf(w, "-- Server version %s\n\n", d.serverVer)
}

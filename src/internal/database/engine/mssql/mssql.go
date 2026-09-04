// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	_ "github.com/denisenkom/go-mssqldb"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

type Dumper struct {
	host       string
	port       int
	user       string
	pass       string
	dbName     string
	db         *sql.DB
	mode       string
	tlsCfg     *config.TLSConfig
	connCfg    *config.ConnectivityConfig
	ctx        context.Context
	Tables     *config.TableFilter
	SchemaOnly bool
}

var mssqlStringLikeTypes = map[string]bool{
	"char":             true,
	"varchar":          true,
	"nchar":            true,
	"nvarchar":         true,
	"text":             true,
	"ntext":            true,
	"xml":              true,
	"uniqueidentifier": true,
	"datetime":         true,
	"datetime2":        true,
	"smalldatetime":    true,
	"date":             true,
	"time":             true,
	"datetimeoffset":   true,
	"money":            true,
	"smallmoney":       true,
	"decimal":          true,
	"numeric":          true,
	"float":            true,
	"real":             true,
	"bit":              true,
	"sysname":          true,
	"sql_variant":      true,
}

var mssqlUnicodeTypes = map[string]bool{
	"nchar":    true,
	"nvarchar": true,
	"ntext":    true,
	"xml":      true,
	"sysname":  true,
}

func (d *Dumper) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	fmt.Fprintf(w, "-- dbbackup MSSQL dump\n")
	fmt.Fprintf(w, "-- Host: %s:%d\n--\n\n", d.host, d.port)

	if len(dbNames) == 1 && strings.ToLower(dbNames[0]) == "all" {
		var err error
		dbNames, err = d.listDatabases()
		if err != nil {
			return err
		}
	}

	ctx := d.ctxOrBg()
	for _, dbName := range dbNames {
		if err := d.dumpDatabase(ctx, w, dbName); err != nil {
			return fmt.Errorf("dump %s: %w", dbName, err)
		}
	}
	return nil
}
func NewDumper(host string, port int, user, pass, dbName string, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 1433
	}
	d := &Dumper{
		host:   host,
		port:   port,
		user:   user,
		pass:   pass,
		dbName: dbName,
		mode:   "database",
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
		connStr := ConnStr(d.user, d.pass, d.host, d.port, d.dbName, d.tlsCfg)
		var err error
		d.db, err = sql.Open("sqlserver", connStr)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		return nil
	}
	ping := func() error {
		if err := d.db.PingContext(ctx); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		return nil
	}
	return common.WithConnectivity(ctx, "mssql", d.connCfg, probe, connect, ping)
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

func (d *Dumper) dumpDatabase(ctx context.Context, w io.Writer, dbName string) error {
	tables, err := d.listTables(ctx, dbName)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "-- Database: %s\n", dbName)

	for _, table := range tables {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(table)
			if !included {
				continue
			}
		}
		common.TraceTable(ctx, dbName, table)
		if err := d.dumpTable(ctx, w, dbName, table); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dumper) dumpTable(ctx context.Context, w io.Writer, dbName, table string) error {
	rows, err := d.db.QueryContext(ctx,
		"SELECT COLUMN_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, IS_NULLABLE, COLUMN_DEFAULT "+
			"FROM "+quoteMSSQLIdent(dbName)+".INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = 'dbo' AND TABLE_NAME = @p1 ORDER BY ORDINAL_POSITION",
		table)
	if err != nil {
		return fmt.Errorf("columns: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE TABLE %s.[dbo].%s (\n", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table))
	var first = true
	colTypes := map[string]string{}
	colIsUnicode := map[string]bool{}
	for rows.Next() {
		var col, dataType, nullable string
		var maxLen *int
		var colDef *string
		if err := rows.Scan(&col, &dataType, &maxLen, &nullable, &colDef); err != nil {
			rows.Close()
			return fmt.Errorf("scan column row: %w", err)
		}
		colTypes[col] = dataType
		colIsUnicode[col] = isUnicodeType(dataType)
		if first {
			first = false
		} else {
			sb.WriteString(",\n")
		}
		sb.WriteString(fmt.Sprintf("    %s %s", quoteMSSQLIdent(col), formatMSSQLType(dataType, maxLen)))
		if nullable == "NO" {
			sb.WriteString(" NOT NULL")
		}
		if colDef != nil {
			sb.WriteString(fmt.Sprintf(" DEFAULT %s", *colDef))
		}
	}
	rows.Close()
	sb.WriteString("\n);\n\n")
	sb.WriteString("GO\n\n")

	fmt.Fprintf(w, "-- Table: %s\n%s", table, sb.String())

	schemaOnly := d.SchemaOnly
	if d.Tables != nil {
		_, so := d.Tables.Apply(table)
		schemaOnly = schemaOnly || so
	}
	if schemaOnly {
		return nil
	}

	dataRows, err := d.db.QueryContext(ctx,
		"SELECT * FROM "+quoteMSSQLIdent(dbName)+".[dbo]."+quoteMSSQLIdent(table))
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer dataRows.Close()

	cols, _ := dataRows.Columns()
	const batchSize = 500
	var rowCount int
	var batchRows int
	for dataRows.Next() {
		vals := make([]any, len(cols))
		valPtrs := make([]any, len(cols))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}
		if err := dataRows.Scan(valPtrs...); err != nil {
			return fmt.Errorf("scan data row: %w", err)
		}

		if batchRows == 0 {
			var qCols []string
			for _, c := range cols {
				qCols = append(qCols, quoteMSSQLIdent(c))
			}
			fmt.Fprintf(w, "INSERT INTO %s.[dbo].%s (%s) VALUES\n", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table), strings.Join(qCols, ", "))
		} else if batchRows < batchSize {
			fmt.Fprintf(w, ",\n")
		}
		batchRows++

		var parts []string
		for i, v := range vals {
			dt := colTypes[cols[i]]
			uni := colIsUnicode[cols[i]]
			switch val := v.(type) {
			case nil:
				parts = append(parts, "NULL")
			case []byte:
				if isStringLikeType(dt) {
					s := string(val)
					if uni {
						parts = append(parts, fmt.Sprintf("N'%s'", escapeMSSQL(s)))
					} else {
						parts = append(parts, fmt.Sprintf("'%s'", escapeMSSQL(s)))
					}
				} else {
					parts = append(parts, fmt.Sprintf("0x%x", val))
				}
			case int64:
				parts = append(parts, fmt.Sprintf("%d", val))
			case float64:
				parts = append(parts, fmt.Sprintf("%v", val))
			case bool:
				if val {
					parts = append(parts, "1")
				} else {
					parts = append(parts, "0")
				}
			case string:
				if uni {
					parts = append(parts, fmt.Sprintf("N'%s'", escapeMSSQL(val)))
				} else {
					parts = append(parts, fmt.Sprintf("'%s'", escapeMSSQL(val)))
				}
			case time.Time:
				parts = append(parts, fmt.Sprintf("'%s'", val.UTC().Format("2006-01-02 15:04:05.0000000")))
			default:
				parts = append(parts, fmt.Sprintf("'%s'", escapeMSSQL(fmt.Sprintf("%v", val))))
			}
		}
		fmt.Fprintf(w, "(%s)", strings.Join(parts, ", "))
		if batchRows >= batchSize {
			fmt.Fprintf(w, ";\nGO\n\n")
			batchRows = 0
		}
		rowCount++
	}
	if batchRows > 0 {
		fmt.Fprintf(w, ";\nGO\n\n")
	}
	return dataRows.Err()
}

func escapeMSSQL(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return s
}

func formatMSSQLType(dataType string, maxLen *int) string {
	dt := strings.ToLower(dataType)
	if maxLen != nil {
		switch dt {
		case "char", "varchar", "nchar", "nvarchar":
			if *maxLen == -1 {
				return strings.ToUpper(dt) + "(MAX)"
			}
			return fmt.Sprintf("%s(%d)", strings.ToUpper(dt), *maxLen)
		}
	}
	return strings.ToUpper(dt)
}

func isStringLikeType(dataType string) bool {
	return mssqlStringLikeTypes[strings.ToLower(dataType)]
}

func isUnicodeType(dataType string) bool {
	return mssqlUnicodeTypes[strings.ToLower(dataType)]
}

func (d *Dumper) listDatabases() ([]string, error) {
	rows, err := d.db.QueryContext(d.ctxOrBg(),
		"SELECT name FROM sys.databases WHERE name NOT IN ('master', 'tempdb', 'model', 'msdb')")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()
	var dbs []string
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, fmt.Errorf("scan database row: %w", err)
		}
		dbs = append(dbs, db)
	}
	return dbs, rows.Err()
}

func (d *Dumper) listTables(ctx context.Context, dbName string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT TABLE_NAME FROM "+quoteMSSQLIdent(dbName)+".INFORMATION_SCHEMA.TABLES WHERE TABLE_TYPE = 'BASE TABLE' AND TABLE_SCHEMA = 'dbo'")
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("scan table row: %w", err)
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}
func quoteMSSQLIdent(s string) string {
	return "[" + strings.ReplaceAll(s, "]", "]]") + "]"
}

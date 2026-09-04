// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

type Dumper struct {
	path       string
	db         *sql.DB
	connCfg    *config.ConnectivityConfig
	ctx        context.Context
	Tables     *config.TableFilter
	SchemaOnly bool
}

func (d *Dumper) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	fmt.Fprintf(w, "-- dbbackup SQLite dump\n")
	fmt.Fprintf(w, "-- File: %s\n--\n\n", d.path)

	tables, err := d.listTables()
	if err != nil {
		return err
	}

	for _, table := range tables {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(table)
			if !included {
				continue
			}
		}
		if err := d.dumpTable(w, table); err != nil {
			return fmt.Errorf("dump %s: %w", table, err)
		}
	}
	return nil
}

func NewDumper(path string) *Dumper {
	return &Dumper{path: path}
}

func (d *Dumper) Open() error {
	return d.OpenContext(context.Background())
}

func (d *Dumper) OpenContext(ctx context.Context) error {
	d.ctx = ctx
	probe := func() error { return common.FilePerms(d.path) }
	connect := func() error {
		dsn := "file:" + d.path + "?mode=ro"
		var err error
		d.db, err = sql.Open("sqlite", dsn)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		return nil
	}
	ping := func() error {
		if err := d.db.PingContext(ctx); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		var one int
		if err := d.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		return nil
	}
	return common.WithConnectivity(ctx, "sqlite", d.connCfg, probe, connect, ping)
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

func (d *Dumper) dumpTable(w io.Writer, table string) error {
	ctx := d.ctxOrBg()
	var createSQL string
	if err := d.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&createSQL); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read create %s: %w", table, err)
	}
	fmt.Fprintf(w, "DROP TABLE IF EXISTS %s;\n", quoteSQLiteIdent(table))
	fmt.Fprintf(w, "-- Table: %s\n%s;\n\n", table, createSQL)

	schemaOnly := d.SchemaOnly
	if d.Tables != nil {
		_, so := d.Tables.Apply(table)
		schemaOnly = schemaOnly || so
	}
	if schemaOnly {
		return nil
	}

	rows, err := d.db.QueryContext(ctx, "SELECT * FROM "+quoteSQLiteIdent(table))
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("columns: %w", err)
	}
	var rowCount int
	for rows.Next() {
		vals := make([]any, len(cols))
		valPtrs := make([]any, len(cols))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}
		if err := rows.Scan(valPtrs...); err != nil {
			return fmt.Errorf("scan data row: %w", err)
		}

		if rowCount == 0 {
			qCols := make([]string, len(cols))
			for i, c := range cols {
				qCols[i] = quoteSQLiteIdent(c)
			}
			fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES\n", quoteSQLiteIdent(table), strings.Join(qCols, ", "))
		} else {
			fmt.Fprintf(w, ",\n")
		}

		var parts []string
		for _, v := range vals {
			switch val := v.(type) {
			case nil:
				parts = append(parts, "NULL")
			case []byte:
				parts = append(parts, fmt.Sprintf("x'%x'", val))
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
				parts = append(parts, fmt.Sprintf("'%s'", escapeSQLite(val)))
			default:
				parts = append(parts, fmt.Sprintf("'%s'", escapeSQLite(fmt.Sprintf("%v", val))))
			}
		}
		fmt.Fprintf(w, "(%s)", strings.Join(parts, ", "))
		rowCount++
	}
	if rowCount > 0 {
		fmt.Fprintf(w, ";\n\n")
	}
	return rows.Err()
}

func escapeSQLite(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	return s
}

func (d *Dumper) listTables() ([]string, error) {
	rows, err := d.db.QueryContext(d.ctxOrBg(), "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
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
func quoteSQLiteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

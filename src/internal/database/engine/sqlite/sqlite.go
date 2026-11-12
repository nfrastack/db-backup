// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

const readOnlyHint = "file exists and is readable but SQLite cannot open it. db-backup needs the directory to be writeable."

func isCantOpen(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unable to open database file")
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".dbb-writetest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func fileReadable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

type Dumper struct {
	path       string
	db         *sql.DB
	connCfg    *config.ConnectivityConfig
	ctx        context.Context
	Tables     *config.TableFilter
	SchemaOnly bool
	server     common.ServerVersion
}

func (d *Dumper) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	start := time.Now()
	log.Debug("sqlite", "backup start", "path", d.path)
	fmt.Fprint(w, common.DumpBanner("--", "SQLite",
		fmt.Sprintf("File: %s  Server: %s", d.path, d.server.Display())))
	fmt.Fprintf(w, "--\n\n")

	tables, err := d.listTables()
	if err != nil {
		return err
	}
	log.Debug("sqlite", "tables listed", "path", d.path, "count", len(tables))
	fmt.Fprintf(w, "PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\n\n")

	rowsByTable := make(map[string]int, len(tables))
	var totalRows int
	for _, table := range tables {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(table)
			if !included {
				log.Trace("sqlite", "table excluded by filter", "table", table)
				continue
			}
		}
		common.TraceTable(d.ctxOrBg(), "", table)
		n, err := d.dumpTable(w, table)
		if err != nil {
			return fmt.Errorf("dump %s: %w", table, err)
		}
		rowsByTable[table] = n
		totalRows += n
	}
	if err := d.dumpQueryStats(w, rowsByTable); err != nil {
		return err
	}
	if err := d.dumpSequences(w, rowsByTable); err != nil {
		return err
	}
	extras, err := d.dumpSchemaObjects(w)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "COMMIT;\n")
	log.Debug("sqlite", "backup done",
		"path", d.path, "tables", len(tables), "rows", totalRows, "extras", extras,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
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
	log.Debug("sqlite", "connect start", "path", d.path)
	if err := common.FilePerms(d.path); err == nil && !dirWritable(filepath.Dir(d.path)) {
		log.Warn("sqlite", "database directory is not writable", "path", d.path, "hint", readOnlyHint, "status", "warn")
	}
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
			if isCantOpen(err) && fileReadable(d.path) {
				return fmt.Errorf("ping: %w (%s)", err, readOnlyHint)
			}
			return fmt.Errorf("ping: %w", err)
		}
		var one int
		if err := d.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		var ver string
		if err := d.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&ver); err != nil {
			log.Trace("sqlite", "server version unavailable", "path", d.path, "error", err.Error())
		} else {
			d.server = ParseServerVersion(ver, runtime.GOARCH)
		}
		log.Debug("sqlite", "connected", "path", d.path, "server", ver)
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

func (d *Dumper) dumpTable(w io.Writer, table string) (int, error) {
	ctx := d.ctxOrBg()
	start := time.Now()
	log.Trace("sqlite", "table start", "table", table)
	var createSQL string
	if err := d.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&createSQL); err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("read create %s: %w", table, err)
	}
	fmt.Fprintf(w, "DROP TABLE IF EXISTS %s;\n", quoteSQLiteIdent(table))
	fmt.Fprintf(w, "-- Table: %s\n%s;\n\n", table, createSQL)

	schemaOnly := d.SchemaOnly
	if d.Tables != nil {
		_, so := d.Tables.Apply(table)
		schemaOnly = schemaOnly || so
	}
	if schemaOnly {
		log.Trace("sqlite", "table done", "table", table, "rows", 0, "schema_only", true)
		return 0, nil
	}

	colRows, err := d.db.QueryContext(ctx, "SELECT * FROM "+quoteSQLiteIdent(table)+" LIMIT 0")
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}
	cols, err := colRows.Columns()
	colRows.Close()
	if err != nil {
		return 0, fmt.Errorf("columns: %w", err)
	}
	genRows, err := d.db.QueryContext(ctx, "SELECT name FROM pragma_table_xinfo('"+strings.ReplaceAll(table, "'", "''")+"') WHERE hidden IN (2, 3)")
	if err != nil {
		return 0, fmt.Errorf("generated columns: %w", err)
	}
	generated := map[string]bool{}
	for genRows.Next() {
		var g string
		if err := genRows.Scan(&g); err != nil {
			genRows.Close()
			return 0, fmt.Errorf("scan generated column: %w", err)
		}
		generated[g] = true
	}
	genRows.Close()
	if err := genRows.Err(); err != nil {
		return 0, fmt.Errorf("generated columns: %w", err)
	}
	insertCols := make([]string, 0, len(cols))
	for _, c := range cols {
		if !generated[c] {
			insertCols = append(insertCols, c)
		}
	}
	if len(insertCols) == 0 {
		return d.dumpDefaultRows(w, table)
	}
	quoted := make([]string, len(insertCols))
	for i, c := range insertCols {
		quoted[i] = "quote(" + quoteSQLiteIdent(c) + ")"
	}
	rows, err := d.db.QueryContext(ctx, "SELECT "+strings.Join(quoted, ", ")+" FROM "+quoteSQLiteIdent(table))
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	var rowCount int
	for rows.Next() {
		vals := make([]sql.NullString, len(insertCols))
		valPtrs := make([]any, len(insertCols))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}
		if err := rows.Scan(valPtrs...); err != nil {
			return rowCount, fmt.Errorf("scan data row: %w", err)
		}

		if rowCount == 0 {
			qCols := make([]string, len(insertCols))
			for i, c := range insertCols {
				qCols[i] = quoteSQLiteIdent(c)
			}
			fmt.Fprintf(w, "INSERT INTO %s (%s) VALUES\n", quoteSQLiteIdent(table), strings.Join(qCols, ", "))
		} else {
			fmt.Fprintf(w, ",\n")
		}

		parts := make([]string, len(insertCols))
		for i, v := range vals {
			if v.Valid {
				parts[i] = v.String
			} else {
				parts[i] = "NULL"
			}
		}
		fmt.Fprintf(w, "(%s)", strings.Join(parts, ", "))
		rowCount++
	}
	if rowCount > 0 {
		fmt.Fprintf(w, ";\n\n")
	}
	if err := rows.Err(); err != nil {
		return rowCount, err
	}
	log.Trace("sqlite", "table done",
		"table", table, "rows", rowCount,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	return rowCount, nil
}

func (d *Dumper) dumpDefaultRows(w io.Writer, table string) (int, error) {
	rows, err := d.db.QueryContext(d.ctxOrBg(), "SELECT 1 FROM "+quoteSQLiteIdent(table))
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}
	defer rows.Close()
	var rowCount int
	for rows.Next() {
		fmt.Fprintf(w, "INSERT INTO %s DEFAULT VALUES;\n", quoteSQLiteIdent(table))
		rowCount++
	}
	if rowCount > 0 {
		fmt.Fprintf(w, "\n")
	}
	if err := rows.Err(); err != nil {
		return rowCount, err
	}
	log.Trace("sqlite", "table done",
		"table", table, "rows", rowCount)
	return rowCount, nil
}

func (d *Dumper) dumpQueryStats(w io.Writer, rowsByTable map[string]int) error {
	var one int
	if err := d.db.QueryRowContext(d.ctxOrBg(), "SELECT 1 FROM sqlite_master WHERE type='table' AND name='sqlite_stat1'").Scan(&one); err != nil {
		return nil
	}
	rows, err := d.db.QueryContext(d.ctxOrBg(), "SELECT tbl, quote(tbl), quote(idx), quote(stat) FROM sqlite_stat1 ORDER BY rowid")
	if err != nil {
		return fmt.Errorf("read query stats: %w", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var tbl sql.NullString
		var qtbl, qidx, qstat string
		if err := rows.Scan(&tbl, &qtbl, &qidx, &qstat); err != nil {
			return fmt.Errorf("scan query stat row: %w", err)
		}
		if tbl.Valid {
			if _, ok := rowsByTable[tbl.String]; !ok {
				continue
			}
		}
		lines = append(lines, "INSERT INTO sqlite_stat1 (tbl, idx, stat) VALUES ("+qtbl+", "+qidx+", "+qstat+");")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(lines) == 0 {
		return nil
	}
	fmt.Fprintf(w, "ANALYZE sqlite_schema;\nDELETE FROM sqlite_stat1;\n")
	for _, l := range lines {
		fmt.Fprintf(w, "%s\n", l)
	}
	fmt.Fprintf(w, "\n")
	return nil
}

func (d *Dumper) dumpSequences(w io.Writer, rowsByTable map[string]int) error {
	var one int
	if err := d.db.QueryRowContext(d.ctxOrBg(), "SELECT 1 FROM sqlite_master WHERE type='table' AND name='sqlite_sequence'").Scan(&one); err != nil {
		return nil
	}
	rows, err := d.db.QueryContext(d.ctxOrBg(), "SELECT name, seq FROM sqlite_sequence ORDER BY name")
	if err != nil {
		return fmt.Errorf("read sequences: %w", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var name string
		var seq int64
		if err := rows.Scan(&name, &seq); err != nil {
			return fmt.Errorf("scan sequence row: %w", err)
		}
		if _, ok := rowsByTable[name]; !ok {
			continue
		}
		q := strings.ReplaceAll(name, "'", "''")
		lines = append(lines,
			fmt.Sprintf("DELETE FROM sqlite_sequence WHERE name='%s';", q),
			fmt.Sprintf("INSERT INTO sqlite_sequence (name, seq) VALUES ('%s', %d);", q, seq))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Fprintf(w, "%s\n", l)
	}
	if len(lines) > 0 {
		fmt.Fprintf(w, "\n")
	}
	return nil
}

func (d *Dumper) dumpSchemaObjects(w io.Writer) (int, error) {
	rows, err := d.db.QueryContext(d.ctxOrBg(), "SELECT type, name, tbl_name, sql FROM sqlite_master WHERE type IN ('index', 'view', 'trigger') AND sql IS NOT NULL ORDER BY rowid")
	if err != nil {
		return 0, fmt.Errorf("list schema objects: %w", err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var typ, name, tbl, objSQL string
		if err := rows.Scan(&typ, &name, &tbl, &objSQL); err != nil {
			return count, fmt.Errorf("scan schema object row: %w", err)
		}
		if d.Tables != nil {
			included, _ := d.Tables.Apply(tbl)
			if !included {
				log.Trace("sqlite", "schema object excluded by filter", "type", typ, "name", name)
				continue
			}
		}
		common.TraceTable(d.ctxOrBg(), "", name)
		switch typ {
		case "index":
			fmt.Fprintf(w, "DROP INDEX IF EXISTS %s;\n", quoteSQLiteIdent(name))
		case "view":
			fmt.Fprintf(w, "DROP VIEW IF EXISTS %s;\n", quoteSQLiteIdent(name))
		case "trigger":
			fmt.Fprintf(w, "DROP TRIGGER IF EXISTS %s;\n", quoteSQLiteIdent(name))
		default:
			continue
		}
		fmt.Fprintf(w, "%s;\n\n", strings.TrimSuffix(strings.TrimSpace(objSQL), ";"))
		count++
	}
	return count, rows.Err()
}

func (d *Dumper) shadowTables() (map[string]bool, error) {
	rows, err := d.db.QueryContext(d.ctxOrBg(), "SELECT name, sql FROM sqlite_master WHERE type='table'")
	if err != nil {
		return nil, fmt.Errorf("list virtual tables: %w", err)
	}
	defer rows.Close()
	suffixes := []string{"_data", "_idx", "_content", "_docsize", "_config", "_segments", "_segdir", "_stat", "_node", "_rowid", "_parent"}
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var createSQL sql.NullString
		if err := rows.Scan(&name, &createSQL); err != nil {
			return nil, fmt.Errorf("scan virtual table row: %w", err)
		}
		if !createSQL.Valid {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(createSQL.String)), "CREATE VIRTUAL TABLE") {
			continue
		}
		for _, s := range suffixes {
			out[name+s] = true
		}
	}
	return out, rows.Err()
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	shadows, err := d.shadowTables()
	if err != nil {
		return nil, err
	}
	kept := tables[:0]
	for _, t := range tables {
		if shadows[t] {
			log.Trace("sqlite", "shadow table skipped", "table", t)
			continue
		}
		kept = append(kept, t)
	}
	return kept, nil
}
func quoteSQLiteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

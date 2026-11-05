// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	_ "github.com/denisenkom/go-mssqldb"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
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
	server     common.ServerVersion
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
	if mode := common.BackupModeFromContext(d.ctxOrBg()); mode == sqlpackageMode {
		return d.dumpSqlpackage(w, dbNames)
	} else if mode != "" {
		return fmt.Errorf("unknown mssql_mode %q", mode)
	}
	start := time.Now()
	log.Debug("mssql", "backup start",
		"host", d.host, "port", d.port,
		"databases", strings.Join(dbNames, ","))
	fmt.Fprint(w, common.DumpBanner("--", "MSSQL",
		fmt.Sprintf("Host: %s:%d  Server: %s", d.host, d.port, d.server.Display())))
	fmt.Fprintf(w, "--\n\n")

	if len(dbNames) == 1 && strings.ToLower(dbNames[0]) == "all" {
		var err error
		dbNames, err = d.listDatabases()
		if err != nil {
			log.Debug("mssql", "expand ALL failed", "host", d.host, "error", err.Error())
			return err
		}
		log.Debug("mssql", "expanded ALL", "count", len(dbNames), "databases", strings.Join(dbNames, ","))
	}

	ctx := d.ctxOrBg()
	for _, dbName := range dbNames {
		if err := d.dumpDatabase(ctx, w, dbName); err != nil {
			return fmt.Errorf("dump %s: %w", dbName, err)
		}
	}
	log.Debug("mssql", "backup done",
		"databases", len(dbNames),
		"elapsed", time.Since(start).Round(time.Millisecond).String())
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
	log.Debug("mssql", "connect start",
		"host", d.host, "port", d.port, "user", d.user,
		"tls", d.tlsCfg != nil)
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
		var ver string
		if err := d.db.QueryRowContext(ctx, "SELECT @@VERSION").Scan(&ver); err != nil {
			log.Trace("mssql", "server version unavailable", "host", d.host, "error", err.Error())
			log.Debug("mssql", "connected", "host", d.host, "port", d.port)
		} else {
			d.server = ParseServerVersion(ver)
			log.Debug("mssql", "connected", "host", d.host, "port", d.port, "server", ver)
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
	dbStart := time.Now()
	tables, err := d.listTables(ctx, dbName)
	if err != nil {
		return err
	}
	log.Debug("mssql", "dumping database",
		"database", dbName, "tables", len(tables))

	fmt.Fprintf(w, "-- Database: %s\n", dbName)

	var dumped []string
	for _, table := range tables {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(table)
			if !included {
				log.Trace("mssql", "table excluded by filter",
					"database", dbName, "table", table)
				continue
			}
		}
		common.TraceTable(ctx, dbName, table)
		if err := d.dumpTable(ctx, w, dbName, table); err != nil {
			return err
		}
		dumped = append(dumped, table)
	}
	if err := d.dumpConstraints(ctx, w, dbName, dumped); err != nil {
		return err
	}
	if err := d.dumpIndexes(ctx, w, dbName, dumped); err != nil {
		return err
	}
	if err := d.dumpViews(ctx, w, dbName); err != nil {
		return err
	}
	if err := d.dumpTriggers(ctx, w, dbName, dumped); err != nil {
		return err
	}
	if err := d.dumpRoutines(ctx, w, dbName); err != nil {
		return err
	}
	log.Debug("mssql", "database done",
		"database", dbName, "tables", len(tables),
		"elapsed", time.Since(dbStart).Round(time.Millisecond).String())
	return nil
}

type keyColumn struct {
	name string
	desc bool
}

func buildKeyConstraint(dbName, table, name, kind string, clustered bool, cols []keyColumn) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		if c.desc {
			parts = append(parts, quoteMSSQLIdent(c.name)+" DESC")
		} else {
			parts = append(parts, quoteMSSQLIdent(c.name)+" ASC")
		}
	}
	how := "NONCLUSTERED"
	if clustered {
		how = "CLUSTERED"
	}
	return fmt.Sprintf("ALTER TABLE %s.[dbo].%s ADD CONSTRAINT %s %s %s (%s);", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table), quoteMSSQLIdent(name), kind, how, strings.Join(parts, ", "))
}

func buildForeignKey(dbName, table, name, refTable string, cols, refCols []string, onDelete, onUpdate string) string {
	q := func(ss []string) []string {
		out := make([]string, 0, len(ss))
		for _, s := range ss {
			out = append(out, quoteMSSQLIdent(s))
		}
		return out
	}
	s := fmt.Sprintf("ALTER TABLE %s.[dbo].%s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s.[dbo].%s (%s)",
		quoteMSSQLIdent(dbName), quoteMSSQLIdent(table), quoteMSSQLIdent(name),
		strings.Join(q(cols), ", "), quoteMSSQLIdent(dbName), quoteMSSQLIdent(refTable), strings.Join(q(refCols), ", "))
	if onDelete != "" {
		s += " ON DELETE " + onDelete
	}
	if onUpdate != "" {
		s += " ON UPDATE " + onUpdate
	}
	return s + ";"
}

func buildCheckConstraint(dbName, table, name, definition string) string {
	return fmt.Sprintf("ALTER TABLE %s.[dbo].%s ADD CONSTRAINT %s CHECK %s;", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table), quoteMSSQLIdent(name), definition)
}

func buildCreateIndex(dbName, table, name string, unique, clustered bool, keys []keyColumn, includes []string, filter string) string {
	uniq := ""
	if unique {
		uniq = "UNIQUE "
	}
	how := "NONCLUSTERED"
	if clustered {
		how = "CLUSTERED"
	}
	keyParts := make([]string, 0, len(keys))
	for _, c := range keys {
		if c.desc {
			keyParts = append(keyParts, quoteMSSQLIdent(c.name)+" DESC")
		} else {
			keyParts = append(keyParts, quoteMSSQLIdent(c.name)+" ASC")
		}
	}
	s := fmt.Sprintf("CREATE %s%s INDEX %s ON %s.[dbo].%s (%s)", uniq, how, quoteMSSQLIdent(name), quoteMSSQLIdent(dbName), quoteMSSQLIdent(table), strings.Join(keyParts, ", "))
	if len(includes) > 0 {
		q := make([]string, 0, len(includes))
		for _, c := range includes {
			q = append(q, quoteMSSQLIdent(c))
		}
		s += " INCLUDE (" + strings.Join(q, ", ") + ")"
	}
	if filter != "" {
		s += " WHERE " + filter
	}
	return s + ";"
}

func orderViewsByDependency(names []string, deps [][2]string) []string {
	inSet := map[string]bool{}
	for _, n := range names {
		inSet[n] = true
	}
	needs := map[string]map[string]bool{}
	for _, dd := range deps {
		a, b := dd[0], dd[1]
		if a == b || !inSet[a] || !inSet[b] {
			continue
		}
		if needs[a] == nil {
			needs[a] = map[string]bool{}
		}
		needs[a][b] = true
	}
	var out []string
	done := map[string]bool{}
	for len(out) < len(names) {
		progress := false
		for _, n := range names {
			if done[n] {
				continue
			}
			ready := true
			for dep := range needs[n] {
				if !done[dep] {
					ready = false
					break
				}
			}
			if ready {
				out = append(out, n)
				done[n] = true
				progress = true
			}
		}
		if !progress {
			for _, n := range names {
				if !done[n] {
					log.Warn("mssql", "view dependency cycle - emitting in name order", "view", n)
					out = append(out, n)
					done[n] = true
				}
			}
		}
	}
	return out
}

func (d *Dumper) tableObjectID(ctx context.Context, dbName, table string) (int64, error) {
	var id int64
	err := d.db.QueryRowContext(ctx,
		"SELECT t.object_id FROM "+quoteMSSQLIdent(dbName)+".sys.tables t JOIN "+quoteMSSQLIdent(dbName)+".sys.schemas s ON s.schema_id = t.schema_id WHERE s.name = 'dbo' AND t.name = @p1",
		table).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("table id %s: %w", table, err)
	}
	return id, nil
}

func (d *Dumper) keyColumns(ctx context.Context, dbName string, objectID int64, indexID int) ([]keyColumn, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT c.name, ic.is_descending_key FROM "+quoteMSSQLIdent(dbName)+".sys.index_columns ic JOIN "+quoteMSSQLIdent(dbName)+".sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id WHERE ic.object_id = @p1 AND ic.index_id = @p2 AND ic.is_included_column = 0 ORDER BY ic.key_ordinal",
		objectID, indexID)
	if err != nil {
		return nil, fmt.Errorf("index columns: %w", err)
	}
	defer rows.Close()
	var cols []keyColumn
	for rows.Next() {
		var c keyColumn
		var name string
		if err := rows.Scan(&name, &c.desc); err != nil {
			return nil, fmt.Errorf("scan index column: %w", err)
		}
		c.name = name
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (d *Dumper) dumpConstraints(ctx context.Context, w io.Writer, dbName string, tables []string) error {
	for _, table := range tables {
		id, err := d.tableObjectID(ctx, dbName, table)
		if err != nil {
			return err
		}
		if id == 0 {
			continue
		}
		var lines []string
		var skippedDisabled int
		var pkName, pkType string
		var pkIndex int
		err = d.db.QueryRowContext(ctx,
			"SELECT kc.name, i.type_desc, kc.unique_index_id FROM "+quoteMSSQLIdent(dbName)+".sys.key_constraints kc JOIN "+quoteMSSQLIdent(dbName)+".sys.indexes i ON i.object_id = kc.parent_object_id AND i.index_id = kc.unique_index_id WHERE kc.parent_object_id = @p1 AND kc.type = 'PK' AND kc.is_ms_shipped = 0",
			id).Scan(&pkName, &pkType, &pkIndex)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("primary key %s: %w", table, err)
		}
		if err == nil {
			cols, err := d.keyColumns(ctx, dbName, id, pkIndex)
			if err != nil {
				return err
			}
			lines = append(lines, buildKeyConstraint(dbName, table, pkName, "PRIMARY KEY", pkType == "CLUSTERED", cols))
		}
		uqRows, err := d.db.QueryContext(ctx,
			"SELECT kc.name, i.type_desc, kc.unique_index_id FROM "+quoteMSSQLIdent(dbName)+".sys.key_constraints kc JOIN "+quoteMSSQLIdent(dbName)+".sys.indexes i ON i.object_id = kc.parent_object_id AND i.index_id = kc.unique_index_id WHERE kc.parent_object_id = @p1 AND kc.type = 'UQ' AND kc.is_ms_shipped = 0 ORDER BY kc.name",
			id)
		if err != nil {
			return fmt.Errorf("unique constraints %s: %w", table, err)
		}
		type uqDef struct {
			name, typ string
			index     int
		}
		var uqs []uqDef
		for uqRows.Next() {
			var u uqDef
			if err := uqRows.Scan(&u.name, &u.typ, &u.index); err != nil {
				return fmt.Errorf("scan unique constraint %s: %w", table, err)
			}
			uqs = append(uqs, u)
		}
		uqRows.Close()
		if err := uqRows.Err(); err != nil {
			return err
		}
		for _, u := range uqs {
			cols, err := d.keyColumns(ctx, dbName, id, u.index)
			if err != nil {
				return err
			}
			lines = append(lines, buildKeyConstraint(dbName, table, u.name, "UNIQUE", u.typ == "CLUSTERED", cols))
		}
		type fkDef struct {
			name, refTable, onDelete, onUpdate string
			cols, refCols                      []string
		}
		fkRows, err := d.db.QueryContext(ctx,
			"SELECT fk.name, fk.delete_referential_action_desc, fk.update_referential_action_desc, pc.name, rc.name, rs.name, rt.name, fk.is_disabled FROM "+quoteMSSQLIdent(dbName)+".sys.foreign_keys fk "+
				"JOIN "+quoteMSSQLIdent(dbName)+".sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id "+
				"JOIN "+quoteMSSQLIdent(dbName)+".sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id "+
				"JOIN "+quoteMSSQLIdent(dbName)+".sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id "+
				"JOIN "+quoteMSSQLIdent(dbName)+".sys.tables rt ON rt.object_id = fkc.referenced_object_id "+
				"JOIN "+quoteMSSQLIdent(dbName)+".sys.schemas rs ON rs.schema_id = rt.schema_id "+
				"WHERE fk.parent_object_id = @p1 AND fk.is_ms_shipped = 0 AND rs.name = 'dbo' ORDER BY fk.name, fkc.constraint_column_id",
			id)
		if err != nil {
			return fmt.Errorf("foreign keys %s: %w", table, err)
		}
		var fks []fkDef
		fkByName := map[string]int{}
		for fkRows.Next() {
			var name, delAct, updAct, pcol, rcol, rschema, rtable string
			var disabled bool
			if err := fkRows.Scan(&name, &delAct, &updAct, &pcol, &rcol, &rschema, &rtable, &disabled); err != nil {
				return fmt.Errorf("scan foreign key %s: %w", table, err)
			}
			if disabled {
				skippedDisabled++
				continue
			}
			if at, ok := fkByName[name]; ok {
				fks[at].cols = append(fks[at].cols, pcol)
				fks[at].refCols = append(fks[at].refCols, rcol)
				continue
			}
			onDel, onUpd := referentialAction(delAct), referentialAction(updAct)
			fkByName[name] = len(fks)
			fks = append(fks, fkDef{name: name, refTable: rtable, onDelete: onDel, onUpdate: onUpd, cols: []string{pcol}, refCols: []string{rcol}})
		}
		fkRows.Close()
		if err := fkRows.Err(); err != nil {
			return err
		}
		for _, fk := range fks {
			lines = append(lines, buildForeignKey(dbName, table, fk.name, fk.refTable, fk.cols, fk.refCols, fk.onDelete, fk.onUpdate))
		}
		ccRows, err := d.db.QueryContext(ctx,
			"SELECT name, definition FROM "+quoteMSSQLIdent(dbName)+".sys.check_constraints WHERE parent_object_id = @p1 AND is_ms_shipped = 0 AND is_disabled = 0",
			id)
		if err != nil {
			return fmt.Errorf("check constraints %s: %w", table, err)
		}
		for ccRows.Next() {
			var name, definition string
			if err := ccRows.Scan(&name, &definition); err != nil {
				return fmt.Errorf("scan check constraint %s: %w", table, err)
			}
			lines = append(lines, buildCheckConstraint(dbName, table, name, definition))
		}
		ccRows.Close()
		if err := ccRows.Err(); err != nil {
			return err
		}
		if skippedDisabled > 0 {
			log.Warn("mssql", "skipping disabled constraints - restore leaves them enabled or absent", "database", dbName, "table", table, "count", skippedDisabled)
		}
		if len(lines) > 0 {
			fmt.Fprintf(w, "\n-- Constraints: %s\n", table)
			for _, l := range lines {
				fmt.Fprintf(w, "%s\nGO\n", l)
			}
		}
	}
	return nil
}

func referentialAction(desc string) string {
	if desc == "NO_ACTION" {
		return ""
	}
	return strings.ReplaceAll(desc, "_", " ")
}

func (d *Dumper) dumpIndexes(ctx context.Context, w io.Writer, dbName string, tables []string) error {
	for _, table := range tables {
		id, err := d.tableObjectID(ctx, dbName, table)
		if err != nil {
			return err
		}
		if id == 0 {
			continue
		}
		rows, err := d.db.QueryContext(ctx,
			"SELECT i.index_id, i.name, i.is_unique, i.type, i.has_filter, i.filter_definition FROM "+quoteMSSQLIdent(dbName)+".sys.indexes i WHERE i.object_id = @p1 AND i.is_primary_key = 0 AND i.is_unique_constraint = 0 AND i.index_id > 0 AND i.is_hypothetical = 0 ORDER BY i.name",
			id)
		if err != nil {
			return fmt.Errorf("indexes %s: %w", table, err)
		}
		type idxCol struct {
			name                 string
			desc, included       bool
			keyOrdinal, columnID int
		}
		type idxDef struct {
			id        int
			name      string
			unique    bool
			indexType int
			filter    string
			keys      []keyColumn
			includes  []string
		}
		var idxs []idxDef
		for rows.Next() {
			var ix idxDef
			var hasFilter bool
			var filterDef sql.NullString
			if err := rows.Scan(&ix.id, &ix.name, &ix.unique, &ix.indexType, &hasFilter, &filterDef); err != nil {
				return fmt.Errorf("scan index %s: %w", table, err)
			}
			if hasFilter && filterDef.Valid {
				ix.filter = filterDef.String
			}
			if ix.indexType != 1 && ix.indexType != 2 {
				log.Trace("mssql", "skipping non-rowstore index", "database", dbName, "table", table, "index", ix.name, "type", ix.indexType)
				continue
			}
			idxs = append(idxs, ix)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var lines []string
		for i := range idxs {
			crows, err := d.db.QueryContext(ctx,
				"SELECT c.name, ic.is_descending_key, ic.is_included_column, ic.key_ordinal, ic.index_column_id FROM "+quoteMSSQLIdent(dbName)+".sys.index_columns ic JOIN "+quoteMSSQLIdent(dbName)+".sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id WHERE ic.object_id = @p1 AND ic.index_id = @p2 ORDER BY ic.index_column_id",
				id, idxs[i].id)
			if err != nil {
				return fmt.Errorf("index columns %s.%s: %w", table, idxs[i].name, err)
			}
			var keyOrds []int
			for crows.Next() {
				var col idxCol
				var name string
				if err := crows.Scan(&name, &col.desc, &col.included, &col.keyOrdinal, &col.columnID); err != nil {
					return fmt.Errorf("scan index column %s.%s: %w", table, idxs[i].name, err)
				}
				col.name = name
				if col.included {
					idxs[i].includes = append(idxs[i].includes, name)
				} else {
					idxs[i].keys = append(idxs[i].keys, keyColumn{name: name, desc: col.desc})
					keyOrds = append(keyOrds, col.keyOrdinal)
				}
			}
			crows.Close()
			if err := crows.Err(); err != nil {
				return err
			}
			sort.Slice(idxs[i].keys, func(a, b int) bool { return keyOrds[a] < keyOrds[b] })
			lines = append(lines, buildCreateIndex(dbName, table, idxs[i].name, idxs[i].unique, idxs[i].indexType == 1, idxs[i].keys, idxs[i].includes, idxs[i].filter))
		}
		if len(lines) > 0 {
			fmt.Fprintf(w, "\n-- Indexes: %s\n", table)
			for _, l := range lines {
				fmt.Fprintf(w, "%s\nGO\n", l)
			}
		}
	}
	return nil
}

func (d *Dumper) dumpViews(ctx context.Context, w io.Writer, dbName string) error {
	rows, err := d.db.QueryContext(ctx,
		"SELECT v.name, m.definition FROM "+quoteMSSQLIdent(dbName)+".sys.views v JOIN "+quoteMSSQLIdent(dbName)+".sys.sql_modules m ON m.object_id = v.object_id WHERE v.is_ms_shipped = 0 ORDER BY v.name")
	if err != nil {
		return fmt.Errorf("views: %w", err)
	}
	defer rows.Close()
	defs := map[string]string{}
	var names []string
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return fmt.Errorf("scan view: %w", err)
		}
		if d.Tables != nil {
			included, _ := d.Tables.Apply(name)
			if !included {
				log.Trace("mssql", "view excluded by filter", "database", dbName, "view", name)
				continue
			}
		}
		defs[name] = definition
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	depRows, err := d.db.QueryContext(ctx,
		"SELECT rv.name, vv.name FROM "+quoteMSSQLIdent(dbName)+".sys.sql_expression_dependencies d JOIN "+quoteMSSQLIdent(dbName)+".sys.views rv ON rv.object_id = d.referencing_id JOIN "+quoteMSSQLIdent(dbName)+".sys.views vv ON vv.object_id = d.referenced_id WHERE d.referencing_class = 1 AND d.referenced_class = 1 AND d.referencing_id <> d.referenced_id")
	if err != nil {
		return fmt.Errorf("view dependencies: %w", err)
	}
	defer depRows.Close()
	var deps [][2]string
	for depRows.Next() {
		var a, b string
		if err := depRows.Scan(&a, &b); err != nil {
			return fmt.Errorf("scan view dependency: %w", err)
		}
		deps = append(deps, [2]string{a, b})
	}
	if err := depRows.Err(); err != nil {
		return err
	}
	for _, name := range orderViewsByDependency(names, deps) {
		fmt.Fprintf(w, "\n-- View: %s\nDROP VIEW IF EXISTS %s;\nGO\n%s;\nGO\n\n", name, quoteMSSQLIdent(name), strings.TrimSuffix(strings.TrimSpace(defs[name]), ";"))
	}
	return nil
}

func (d *Dumper) dumpTriggers(ctx context.Context, w io.Writer, dbName string, tables []string) error {
	kept := map[string]bool{}
	for _, t := range tables {
		kept[t] = true
	}
	rows, err := d.db.QueryContext(ctx,
		"SELECT t.name, m.definition, pt.name FROM "+quoteMSSQLIdent(dbName)+".sys.triggers t JOIN "+quoteMSSQLIdent(dbName)+".sys.sql_modules m ON m.object_id = t.object_id JOIN "+quoteMSSQLIdent(dbName)+".sys.tables pt ON pt.object_id = t.parent_id WHERE t.is_ms_shipped = 0 AND t.parent_class = 1 ORDER BY t.name")
	if err != nil {
		return fmt.Errorf("triggers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, definition, parent string
		if err := rows.Scan(&name, &definition, &parent); err != nil {
			return fmt.Errorf("scan trigger: %w", err)
		}
		if !kept[parent] {
			log.Trace("mssql", "trigger excluded by filter", "database", dbName, "trigger", name)
			continue
		}
		common.TraceTable(ctx, dbName, name)
		fmt.Fprintf(w, "\n-- Trigger: %s (on %s)\nDROP TRIGGER IF EXISTS %s;\nGO\n%s;\nGO\n\n", name, parent, quoteMSSQLIdent(name), strings.TrimSuffix(strings.TrimSpace(definition), ";"))
	}
	return rows.Err()
}

func (d *Dumper) dumpRoutines(ctx context.Context, w io.Writer, dbName string) error {
	rows, err := d.db.QueryContext(ctx,
		"SELECT o.name, o.type, m.definition FROM "+quoteMSSQLIdent(dbName)+".sys.objects o JOIN "+quoteMSSQLIdent(dbName)+".sys.sql_modules m ON m.object_id = o.object_id WHERE o.type IN ('P', 'FN', 'IF', 'TF') AND o.is_ms_shipped = 0 ORDER BY o.type, o.name")
	if err != nil {
		return fmt.Errorf("routines: %w", err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var name, typ, definition string
		if err := rows.Scan(&name, &typ, &definition); err != nil {
			return fmt.Errorf("scan routine: %w", err)
		}
		kind := "FUNCTION"
		if typ == "P" {
			kind = "PROCEDURE"
		}
		common.TraceTable(ctx, dbName, name)
		fmt.Fprintf(w, "\n-- Routine: %s\nDROP %s IF EXISTS %s;\nGO\n%s;\nGO\n\n", name, kind, quoteMSSQLIdent(name), strings.TrimSuffix(strings.TrimSpace(definition), ";"))
		count++
	}
	return rows.Err()
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
	flags, err := d.columnFlags(ctx, dbName, table)
	if err != nil {
		rows.Close()
		return err
	}
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
		fl := flags[col]
		if fl.computed {
			sb.WriteString(fmt.Sprintf("    %s AS (%s)", quoteMSSQLIdent(col), fl.definition))
			if fl.persisted {
				sb.WriteString(" PERSISTED")
			}
		} else {
			sb.WriteString(fmt.Sprintf("    %s %s", quoteMSSQLIdent(col), formatMSSQLType(dataType, maxLen)))
			if nullable == "NO" {
				sb.WriteString(" NOT NULL")
			}
			if colDef != nil {
				sb.WriteString(fmt.Sprintf(" DEFAULT %s", *colDef))
			}
			if fl.identity {
				sb.WriteString(fmt.Sprintf(" IDENTITY(%s, %s)", fl.seed, fl.step))
			}
		}
	}
	rows.Close()
	computed := map[string]bool{}
	for name, fl := range flags {
		if fl.computed {
			computed[name] = true
		}
	}
	sb.WriteString("\n);\n\n")
	sb.WriteString("GO\n\n")

	fmt.Fprintf(w, "DROP TABLE IF EXISTS %s.[dbo].%s;\nGO\n", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table))
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
	insertCols := insertableMSSQLColumns(cols, computed)
	keepSet := make(map[string]bool, len(insertCols))
	for _, c := range insertCols {
		keepSet[c] = true
	}
	identityInsert := false
	for _, c := range insertCols {
		if flags[c].identity {
			identityInsert = true
			break
		}
	}
	if len(insertCols) == 0 {
		dataRows.Close()
		return d.dumpDefaultRows(ctx, w, dbName, table)
	}
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
			if rowCount == 0 && identityInsert {
				fmt.Fprintf(w, "SET IDENTITY_INSERT %s.[dbo].%s ON;\n", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table))
			}
			var qCols []string
			for _, c := range insertCols {
				qCols = append(qCols, quoteMSSQLIdent(c))
			}
			fmt.Fprintf(w, "INSERT INTO %s.[dbo].%s (%s) VALUES\n", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table), strings.Join(qCols, ", "))
		} else if batchRows < batchSize {
			fmt.Fprintf(w, ",\n")
		}
		batchRows++

		var parts []string
		for i, v := range vals {
			if !keepSet[cols[i]] {
				continue
			}
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
		fmt.Fprintf(w, ";\nGO\n")
		if identityInsert {
			fmt.Fprintf(w, "SET IDENTITY_INSERT %s.[dbo].%s OFF;\n", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table))
		}
		fmt.Fprintf(w, "\n")
	}
	return dataRows.Err()
}

type columnFlag struct {
	computed   bool
	definition string
	persisted  bool
	identity   bool
	seed       string
	step       string
}

func (d *Dumper) columnFlags(ctx context.Context, dbName, table string) (map[string]columnFlag, error) {
	rows, err := d.db.QueryContext(ctx,
		"SELECT c.name, c.is_computed, cc.definition, cc.is_persisted, c.is_identity, CONVERT(varchar(40), ic.seed_value), CONVERT(varchar(40), ic.increment_value) FROM "+quoteMSSQLIdent(dbName)+".sys.columns c "+
			"JOIN "+quoteMSSQLIdent(dbName)+".sys.tables t ON t.object_id = c.object_id "+
			"JOIN "+quoteMSSQLIdent(dbName)+".sys.schemas s ON s.schema_id = t.schema_id "+
			"LEFT JOIN "+quoteMSSQLIdent(dbName)+".sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id "+
			"LEFT JOIN "+quoteMSSQLIdent(dbName)+".sys.identity_columns ic ON ic.object_id = c.object_id AND ic.column_id = c.column_id "+
			"WHERE s.name = 'dbo' AND t.name = @p1",
		table)
	if err != nil {
		return nil, fmt.Errorf("column flags: %w", err)
	}
	defer rows.Close()
	out := map[string]columnFlag{}
	for rows.Next() {
		var name string
		var fl columnFlag
		var definition, seed, step sql.NullString
		var persisted sql.NullBool
		if err := rows.Scan(&name, &fl.computed, &definition, &persisted, &fl.identity, &seed, &step); err != nil {
			return nil, fmt.Errorf("scan column flag: %w", err)
		}
		if definition.Valid {
			fl.definition = definition.String
		}
		fl.persisted = persisted.Valid && persisted.Bool
		if seed.Valid {
			fl.seed = seed.String
		}
		if step.Valid {
			fl.step = step.String
		}
		out[name] = fl
	}
	return out, rows.Err()
}

func insertableMSSQLColumns(cols []string, computed map[string]bool) []string {
	kept := make([]string, 0, len(cols))
	for _, c := range cols {
		if !computed[c] {
			kept = append(kept, c)
		}
	}
	return kept
}

func (d *Dumper) dumpDefaultRows(ctx context.Context, w io.Writer, dbName, table string) error {
	rows, err := d.db.QueryContext(ctx, "SELECT 1 FROM "+quoteMSSQLIdent(dbName)+".[dbo]."+quoteMSSQLIdent(table))
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	defer rows.Close()
	var rowCount int
	for rows.Next() {
		fmt.Fprintf(w, "INSERT INTO %s.[dbo].%s DEFAULT VALUES;\nGO\n", quoteMSSQLIdent(dbName), quoteMSSQLIdent(table))
		rowCount++
	}
	if rowCount > 0 {
		fmt.Fprintf(w, "\n")
	}
	return rows.Err()
}

func escapeMSSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
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

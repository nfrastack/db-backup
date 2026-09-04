// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

type Dumper struct {
	host       string
	port       int
	user       string
	pass       string
	dbname     string
	conn       *pgx.Conn
	serverVer  string
	tlsCfg     *config.TLSConfig
	connCfg    *config.ConnectivityConfig
	ctx        context.Context
	SplitDB    bool
	Tables     *config.TableFilter
	SchemaOnly bool
}

func (d *Dumper) Close() error {
	if d.conn != nil {
		return d.conn.Close(context.Background())
	}
	return nil
}

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	d.writeHeader(w, dbNames)

	for _, dbName := range dbNames {
		if strings.ToLower(dbName) == "all" {
			if err := d.dumpAll(w); err != nil {
				return err
			}
			continue
		}
		if d.dbname != dbName && d.dbname != "" && dbName != "" {
			nd := d.cloneForDB(dbName)
			if err := nd.OpenContext(d.ctxOrBg()); err != nil {
				return fmt.Errorf("connect %s: %w", dbName, err)
			}
			err := nd.dumpDatabase(w, dbName)
			nd.Close()
			if err != nil {
				return fmt.Errorf("dump %s: %w", dbName, err)
			}
			continue
		}
		if err := d.dumpDatabase(w, dbName); err != nil {
			return fmt.Errorf("dump %s: %w", dbName, err)
		}
	}

	d.writeFooter(w)
	return nil
}

func (d *Dumper) cloneForDB(db string) *Dumper {
	return &Dumper{
		host:       d.host,
		port:       d.port,
		user:       d.user,
		pass:       d.pass,
		dbname:     db,
		tlsCfg:     d.tlsCfg,
		connCfg:    d.connCfg,
		ctx:        d.ctxOrBg(),
		Tables:     d.Tables,
		SchemaOnly: d.SchemaOnly,
		serverVer:  d.serverVer,
	}
}

func (d *Dumper) DumpGlobals(w io.Writer) error {
	if d.ctx == nil {
		d.ctx = context.Background()
	}
	ctx := d.ctx

	fmt.Fprintf(w, "-- dbbackup PostgreSQL globals dump\n")
	fmt.Fprintf(w, "-- Host: %s  Server: %s\n--\n\n", d.host, d.serverVer)

	rows, err := d.conn.Query(ctx,
		"SELECT rolname, rolsuper, rolinherit, rolcreaterole, rolcreatedb, rolcanlogin, rolreplication, rolconnlimit, rolvaliduntil FROM pg_roles WHERE rolname != 'postgres' ORDER BY rolname")
	if err != nil {
		return fmt.Errorf("query roles: %w", err)
	}
	for rows.Next() {
		var name string
		var super, inherit, createrole, createdb, canlogin, replication bool
		var connlimit int
		var validuntil *time.Time
		if err := rows.Scan(&name, &super, &inherit, &createrole, &createdb, &canlogin, &replication, &connlimit, &validuntil); err != nil {
			rows.Close()
			return fmt.Errorf("scan role row: %w", err)
		}

		opts := ""
		if super {
			opts += " SUPERUSER"
		} else {
			opts += " NOSUPERUSER"
		}
		if inherit {
			opts += " INHERIT"
		} else {
			opts += " NOINHERIT"
		}
		if createrole {
			opts += " CREATEROLE"
		} else {
			opts += " NOCREATEROLE"
		}
		if createdb {
			opts += " CREATEDB"
		} else {
			opts += " NOCREATEDB"
		}
		if canlogin {
			opts += " LOGIN"
		} else {
			opts += " NOLOGIN"
		}
		if replication {
			opts += " REPLICATION"
		} else {
			opts += " NOREPLICATION"
		}
		if connlimit > 0 {
			opts += fmt.Sprintf(" CONNECTION LIMIT %d", connlimit)
		}

		fmt.Fprintf(w, "CREATE ROLE %s WITH%s;\n", quotePGIdent(name), opts)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan roles: %w", err)
	}

	memRows, err := d.conn.Query(ctx,
		"SELECT r1.rolname, r2.rolname FROM pg_auth_members m JOIN pg_roles r1 ON m.member = r1.oid JOIN pg_roles r2 ON m.roleid = r2.oid WHERE r2.rolname != 'postgres'")
	if err == nil {
		for memRows.Next() {
			var member, role string
			if err := memRows.Scan(&member, &role); err != nil {
				memRows.Close()
				return fmt.Errorf("scan membership row: %w", err)
			}
			fmt.Fprintf(w, "GRANT %s TO %s;\n", quotePGIdent(role), quotePGIdent(member))
		}
		memRows.Close()
		if err := memRows.Err(); err != nil {
			return fmt.Errorf("scan membership: %w", err)
		}
	}

	fmt.Fprintf(w, "\n-- Globals dump completed\n")
	return rows.Err()
}

func NewDumper(host string, port int, user, pass, dbname string, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 5432
	}
	d := &Dumper{
		host:   host,
		port:   port,
		user:   user,
		pass:   pass,
		dbname: dbname,
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
		connStr := ConnStr(d.user, d.pass, d.host, d.port, d.dbname, d.tlsCfg)
		var err error
		connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		d.conn, err = pgx.Connect(connectCtx, connStr)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		return nil
	}
	ping := func() error {
		var ver string
		if err := d.conn.QueryRow(ctx, "SELECT VERSION()").Scan(&ver); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		d.serverVer = ver
		return nil
	}
	return common.WithConnectivity(ctx, "postgres", d.connCfg, probe, connect, ping)
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

func (d *Dumper) copyData(w io.Writer, schema, table string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT * FROM "+quotePGIdent(schema)+"."+quotePGIdent(table))
	if err != nil {
		return fmt.Errorf("select %s.%s: %w", schema, table, err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	colNames := make([]string, len(fields))
	for i, f := range fields {
		colNames[i] = string(f.Name)
	}

	quotedCols := make([]string, len(colNames))
	for i, c := range colNames {
		quotedCols[i] = quotePGIdent(c)
	}
	fmt.Fprintf(w, "COPY %s.%s (%s) FROM stdin;\n", quotePGIdent(schema), quotePGIdent(table), strings.Join(quotedCols, ", "))

	var rowCount int
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		line := make([]string, len(values))
		for i, val := range values {
			switch v := val.(type) {
			case nil:
				line[i] = "\\N"
			case []byte:
				line[i] = escapePgCopy(string(v))
			case string:
				line[i] = escapePgCopy(v)
			case int64:
				line[i] = fmt.Sprintf("%d", v)
			case float64:
				line[i] = fmt.Sprintf("%v", v)
			case time.Time:
				line[i] = v.Format("2006-01-02 15:04:05.999999-07")
			case bool:
				if v {
					line[i] = "t"
				} else {
					line[i] = "f"
				}
			case fmt.Stringer:
				line[i] = escapePgCopy(v.String())
			default:
				line[i] = escapePgCopy(fmt.Sprintf("%v", v))
			}
		}
		fmt.Fprintf(w, "%s\n", strings.Join(line, "\t"))
		rowCount++
	}

	if err := rows.Err(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\\.\n\n")
	return nil
}
func (d *Dumper) ctxOrBg() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

func (d *Dumper) dumpAll(w io.Writer) error {
	dbs, err := d.listDatabases()
	if err != nil {
		return err
	}
	for _, db := range dbs {
		if isPgSystemDB(db) {
			continue
		}
		nd := d.cloneForDB(db)
		if err := nd.OpenContext(d.ctxOrBg()); err != nil {
			return fmt.Errorf("connect %s: %w", db, err)
		}
		if err := nd.dumpDatabase(w, db); err != nil {
			nd.Close()
			return err
		}
		nd.Close()
	}
	return nil
}

func (d *Dumper) dumpDatabase(w io.Writer, dbName string) error {
	fmt.Fprintf(w, "\n-- Database: %s\n", dbName)

	tables, err := d.listTables(dbName)
	if err != nil {
		return err
	}

	for _, table := range tables {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(table)
			if !included {
				if _, bare, ok := strings.Cut(table, "."); ok {
					included, _ = d.Tables.Apply(bare)
				}
			}
			if !included {
				continue
			}
		}
		common.TraceTable(d.ctxOrBg(), dbName, table)
		if err := d.dumpTable(w, dbName, table); err != nil {
			return err
		}
	}

	if err := d.dumpViews(w, dbName); err != nil {
		return err
	}
	if err := d.dumpFunctions(w, dbName); err != nil {
		return err
	}
	return nil
}

func (d *Dumper) dumpFunctions(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT proname, pg_get_functiondef(oid) FROM pg_proc "+
			"WHERE pronamespace NOT IN ('pg_catalog'::regnamespace, 'information_schema'::regnamespace) "+
			"AND prokind IN ('f', 'p')")
	if err != nil {
		return fmt.Errorf("query functions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return fmt.Errorf("scan function row: %w", err)
		}
		fmt.Fprintf(w, "\n-- Function: %s\n%s\n\n", name, def)
	}
	return rows.Err()
}

func (d *Dumper) dumpSequencesForTable(w io.Writer, schema, table string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname || '.' || s.relname, s.relname "+
			"FROM pg_catalog.pg_class s "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = s.relnamespace "+
			"JOIN pg_catalog.pg_depend d ON d.objid = s.oid "+
			"JOIN pg_catalog.pg_class c ON c.oid = d.refobjid "+
			"JOIN pg_catalog.pg_namespace ns ON ns.oid = c.relnamespace "+
			"WHERE s.relkind = 'S' AND d.refobjsubid > 0 "+
			"AND ns.nspname = $1 AND c.relname = $2", schema, table)
	if err != nil {
		return nil
	}

	type seqInfo struct {
		name    string
		relname string
	}
	var seqs []seqInfo

	for rows.Next() {
		var s seqInfo
		if err := rows.Scan(&s.name, &s.relname); err != nil {
			continue
		}
		if s.name != "" && s.name != "." {
			seqs = append(seqs, s)
		}
	}
	rows.Close()

	for _, s := range seqs {
		var seqdef string
		err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT 'CREATE SEQUENCE ' || $1 || ' START ' || ps.seqstart || "+
				"' INCREMENT BY ' || ps.seqincrement || ' MAXVALUE ' || ps.seqmax || "+
				"' MINVALUE ' || ps.seqmin || ' CACHE ' || ps.seqcache || "+
				"CASE WHEN ps.seqcycle THEN ' CYCLE' ELSE ' NO CYCLE' END "+
				"FROM pg_catalog.pg_sequence ps "+
				"JOIN pg_catalog.pg_class c ON c.oid = ps.seqrelid "+
				"WHERE c.relname = $2", s.name, s.relname).Scan(&seqdef)
		if err == nil && seqdef != "" {
			fmt.Fprintf(w, "%s;\n", seqdef)
		}
	}
	return rows.Err()
}

func (d *Dumper) dumpTable(w io.Writer, dbName, table string) error {
	parts := strings.SplitN(table, ".", 2)
	schema := parts[0]
	tableName := parts[1]

	if err := d.dumpSequencesForTable(w, schema, tableName); err != nil {
		return err
	}

	createSQL, err := d.getCreateTable(schema, tableName)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\n-- Table: %s.%s\n%s\n\n", schema, tableName, createSQL)

	schemaOnly := d.SchemaOnly
	if d.Tables != nil {
		_, so := d.Tables.Apply(table)
		if !so {
			if _, bare, ok := strings.Cut(table, "."); ok {
				_, so = d.Tables.Apply(bare)
			}
		}
		schemaOnly = schemaOnly || so
	}
	if schemaOnly {
		return nil
	}

	if err := d.copyData(w, schema, tableName); err != nil {
		return err
	}

	if err := d.setSequenceValues(w, schema, tableName); err != nil {
		return err
	}

	return nil
}

func (d *Dumper) dumpViews(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT table_schema, table_name, view_definition FROM information_schema.views "+
			"WHERE table_schema NOT IN ('pg_catalog', 'information_schema')")
	if err != nil {
		return fmt.Errorf("query views: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, name, def string
		if err := rows.Scan(&schema, &name, &def); err != nil {
			return fmt.Errorf("scan view row: %w", err)
		}
		if def == "" {
			continue
		}
		fmt.Fprintf(w, "\n-- View: %s.%s\n", schema, name)
		fmt.Fprintf(w, "CREATE OR REPLACE VIEW %s.%s AS\n%s;\n\n", schema, name, def)
	}
	return rows.Err()
}

func escapePGLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
func escapePgCopy(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func (d *Dumper) getCheckConstraints(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"WHERE n.nspname = $1 AND t.relname = $2 AND c.contype = 'c' "+
			"ORDER BY c.conname", schema, table)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var conname, def string
		if err := rows.Scan(&conname, &def); err != nil {
			return "", err
		}
		if def == "" {
			continue
		}
		fmt.Fprintf(&sb, "ALTER TABLE %s.%s ADD CONSTRAINT %s %s;\n",
			quotePGIdent(schema), quotePGIdent(table), quotePGIdent(conname), def)
	}
	return sb.String(), rows.Err()
}
func (d *Dumper) getCreateTable(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT a.attname, "+
			"       format_type(a.atttypid, a.atttypmod) AS typ, "+
			"       a.attnotnull, "+
			"       pg_get_expr(d.adbin, d.adrelid) AS defexpr, "+
			"       a.attidentity, a.attgenerated "+
			"FROM pg_catalog.pg_attribute a "+
			"JOIN pg_catalog.pg_class c ON c.oid = a.attrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum AND a.atthasdef "+
			"WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped "+
			"ORDER BY a.attnum", schema, table)
	if err != nil {
		return "", fmt.Errorf("get columns: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP TABLE IF EXISTS %s.%s;\n", quotePGIdent(schema), quotePGIdent(table))
	fmt.Fprintf(&sb, "CREATE TABLE %s.%s (", quotePGIdent(schema), quotePGIdent(table))
	var first = true
	for rows.Next() {
		var col, typ, identity, generated string
		var defExpr sql.NullString
		var notNull bool
		if err := rows.Scan(&col, &typ, &notNull, &defExpr, &identity, &generated); err != nil {
			return "", fmt.Errorf("scan column row: %w", err)
		}
		if first {
			first = false
		} else {
			sb.WriteString(",")
		}
		sb.WriteString("\n    " + quotePGIdent(col) + " " + typ)
		if notNull {
			sb.WriteString(" NOT NULL")
		}
		defStr := ""
		if defExpr.Valid {
			defStr = defExpr.String
		}
		switch {
		case generated == "s":
			if defStr != "" {
				sb.WriteString(" GENERATED ALWAYS AS (" + defStr + ") STORED")
			}
		case identity == "a":
			sb.WriteString(" GENERATED ALWAYS AS IDENTITY")
			if defStr != "" {
				sb.WriteString(" (" + defStr + ")")
			}
		case identity == "d":
			sb.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
			if defStr != "" {
				sb.WriteString(" (" + defStr + ")")
			}
		case defStr != "":
			sb.WriteString(" DEFAULT " + defStr)
		}
	}
	sb.WriteString("\n)")

	partitionBy, err := d.getPartitionInfo(schema, table)
	if err == nil && partitionBy != "" {
		sb.WriteString(" " + partitionBy)
	}
	sb.WriteString(";")

	if pk, err := d.getPrimaryKey(schema, table); err == nil && pk != "" {
		sb.WriteString("\n" + pk)
	}
	if idxSQL, err := d.getIndexes(schema, table); err == nil {
		sb.WriteString("\n" + idxSQL)
	}
	if checkSQL, err := d.getCheckConstraints(schema, table); err == nil {
		sb.WriteString("\n" + checkSQL)
	}
	if fkSQL, err := d.getForeignKeys(schema, table); err == nil {
		sb.WriteString("\n" + fkSQL)
	}

	return sb.String(), rows.Err()
}

func (d *Dumper) getForeignKeys(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"WHERE n.nspname = $1 AND t.relname = $2 AND c.contype = 'f' "+
			"ORDER BY c.conname", schema, table)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var conname, def string
		if err := rows.Scan(&conname, &def); err != nil {
			return "", err
		}
		if def == "" {
			continue
		}
		fmt.Fprintf(&sb, "ALTER TABLE %s.%s ADD CONSTRAINT %s %s;\n",
			quotePGIdent(schema), quotePGIdent(table), quotePGIdent(conname), def)
	}
	return sb.String(), rows.Err()
}

func (d *Dumper) getIndexes(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT i.indexrelid::regclass, pg_get_indexdef(i.indexrelid), i.indisprimary "+
			"FROM pg_catalog.pg_index i "+
			"JOIN pg_catalog.pg_class c ON c.oid = i.indrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary = false",
		schema, table)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var idxName, idxDef string
		var isPrimary bool
		if err := rows.Scan(&idxName, &idxDef, &isPrimary); err != nil {
			return "", fmt.Errorf("scan index row: %w", err)
		}
		_ = isPrimary
		sb.WriteString(idxDef + ";\n")
	}
	return sb.String(), rows.Err()
}

func (d *Dumper) getPartitionInfo(schema, table string) (string, error) {
	var partSQL string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT 'PARTITION BY ' || pg_catalog.pg_get_partkeydef(c.oid) "+
			"FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2 AND c.relispartition = false AND c.relpartbound IS NULL",
		schema, table).Scan(&partSQL)
	return partSQL, err
}
func (d *Dumper) getPrimaryKey(schema, table string) (string, error) {
	var conname, def string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"WHERE n.nspname = $1 AND t.relname = $2 "+
			"  AND c.contype = 'p' LIMIT 1", schema, table).Scan(&conname, &def)
	if err != nil {
		return "", err
	}
	if def == "" {
		return "", nil
	}
	return fmt.Sprintf("ALTER TABLE %s.%s ADD CONSTRAINT %s %s;",
		quotePGIdent(schema), quotePGIdent(table), quotePGIdent(conname), def), nil
}

func isPgSystemDB(name string) bool {
	switch name {
	case "template0", "template1", "postgres":
		return true
	}
	return false
}

func (d *Dumper) listDatabases() ([]string, error) {
	var dbs []string
	rows, err := d.conn.Query(d.ctxOrBg(), "SELECT datname FROM pg_database WHERE datistemplate = false")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, fmt.Errorf("scan database name: %w", err)
		}
		if isPgSystemDB(db) {
			continue
		}
		dbs = append(dbs, db)
	}
	return dbs, rows.Err()
}

func (d *Dumper) listTables(dbName string) ([]string, error) {
	var tables []string
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT table_schema, table_name FROM information_schema.tables "+
			"WHERE table_schema NOT IN ('pg_catalog', 'information_schema') "+
			"AND table_type = 'BASE TABLE' ORDER BY table_schema, table_name")
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, table string
		rows.Scan(&schema, &table)
		tables = append(tables, schema+"."+table)
	}
	return tables, rows.Err()
}

func pgTypeName(base string, charMaxLen, numPrec, numScale *int) string {
	switch {
	case base == "character varying" && charMaxLen != nil:
		return fmt.Sprintf("character varying(%d)", *charMaxLen)
	case base == "character" && charMaxLen != nil:
		return fmt.Sprintf("character(%d)", *charMaxLen)
	case base == "numeric" && numPrec != nil && numScale != nil:
		return fmt.Sprintf("numeric(%d,%d)", *numPrec, *numScale)
	case base == "numeric" && numPrec != nil:
		return fmt.Sprintf("numeric(%d)", *numPrec)
	default:
		return base
	}
}

func quotePGIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteQualifiedPGIdent(qualified string) string {
	parts := strings.Split(qualified, ".")
	for i, p := range parts {
		parts[i] = quotePGIdent(p)
	}
	return strings.Join(parts, ".")
}

func (d *Dumper) setSequenceValues(w io.Writer, schema, table string) error {
	var seqName string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT n.nspname || '.' || s.relname "+
			"FROM pg_catalog.pg_class s "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = s.relnamespace "+
			"JOIN pg_catalog.pg_depend d ON d.objid = s.oid "+
			"JOIN pg_catalog.pg_class c ON c.oid = d.refobjid "+
			"JOIN pg_catalog.pg_namespace ns ON ns.oid = c.relnamespace "+
			"WHERE s.relkind = 'S' AND ns.nspname = $1 AND c.relname = $2 "+
			"LIMIT 1", schema, table).Scan(&seqName)
	if err != nil || seqName == "" {
		return nil
	}

	var lastVal int64
	if err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT last_value FROM "+quoteQualifiedPGIdent(seqName)).Scan(&lastVal); err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("read sequence %s last_value: %w", seqName, err)
	}
	if lastVal > 0 {
		fmt.Fprintf(w, "SELECT pg_catalog.setval('%s', %d);\n", escapePGLiteral(seqName), lastVal)
	}
	return nil
}

func (d *Dumper) writeFooter(w io.Writer) {
	fmt.Fprintf(w, "-- Dump completed\n")
}

func (d *Dumper) writeHeader(w io.Writer, dbNames []string) {
	fmt.Fprintf(w, `-- dbbackup PostgreSQL dump
-- Host: %s  Server: %s
--
`, d.host, d.serverVer)
	fmt.Fprintf(w, "SET statement_timeout = 0;\n")
	fmt.Fprintf(w, "SET lock_timeout = 0;\n")
	fmt.Fprintf(w, "SET idle_in_transaction_session_timeout = 0;\n")
	fmt.Fprintf(w, "SET client_encoding = 'UTF8';\n")
	fmt.Fprintf(w, "SET standard_conforming_strings = on;\n")
	fmt.Fprintf(w, "SELECT pg_catalog.set_config('search_path', '\"$user\", public', false);\n")
	fmt.Fprintf(w, "SET check_function_bodies = false;\n")
	fmt.Fprintf(w, "SET xmloption = content;\n")
	fmt.Fprintf(w, "SET client_min_messages = warning;\n")
	fmt.Fprintf(w, "SET row_security = off;\n\n")
	fmt.Fprintf(w, "-- Server version %s\n\n", d.serverVer)
}

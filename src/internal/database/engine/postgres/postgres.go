// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
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
	start := time.Now()
	log.Debug("postgres", "backup start",
		"host", d.host, "port", d.port, "server", d.serverVer,
		"databases", strings.Join(dbNames, ","))
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
	log.Debug("postgres", "backup done",
		"databases", len(dbNames),
		"elapsed", time.Since(start).Round(time.Millisecond).String())
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
	log.Debug("postgres", "connect start",
		"host", d.host, "port", d.port, "user", d.user,
		"tls", d.tlsCfg != nil)
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
		log.Debug("postgres", "connected",
			"host", d.host, "port", d.port, "server", ver)
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

func (d *Dumper) copyData(w io.Writer, dbName, schema, table string) error {
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

	fallbackCols := map[string]bool{}
	defer func() {
		if len(fallbackCols) > 0 {
			names := make([]string, 0, len(fallbackCols))
			for n := range fallbackCols {
				names = append(names, n)
			}
			sort.Strings(names)
			log.Trace("postgres", "generic COPY formatting", "database", dbName,
				"table", schema+"."+table, "columns", strings.Join(names, ","))
		}
	}()

	var rowCount int
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		line := make([]string, len(values))
		for i, val := range values {
			var oid uint32
			if i < len(fields) {
				oid = fields[i].DataTypeOID
			}
			enc, fallback := encodeCopyValue(val, oid)
			if fallback {
				fallbackCols[colNames[i]] = true
			}
			line[i] = enc
		}
		fmt.Fprintf(w, "%s\n", strings.Join(line, "\t"))
		rowCount++
	}

	if err := rows.Err(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\\.\n\n")
	log.Debug("postgres", "table data done", "database", dbName,
		"table", schema+"."+table, "rows", rowCount)
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
	dbStart := time.Now()
	fmt.Fprintf(w, "\n-- Database: %s\n", dbName)

	tables, err := d.listTables(dbName)
	if err != nil {
		return err
	}
	log.Debug("postgres", "dumping database",
		"database", dbName, "tables", len(tables))

	var included []string
	for _, table := range tables {
		if d.Tables != nil {
			ok, _ := d.Tables.Apply(table)
			if !ok {
				if _, bare, cut := strings.Cut(table, "."); cut {
					ok, _ = d.Tables.Apply(bare)
				}
			}
			if !ok {
				log.Trace("postgres", "table excluded by filter", "database", dbName, "table", table)
				continue
			}
		}
		included = append(included, table)
	}

	if err := d.dumpDrops(w, dbName, included); err != nil {
		return err
	}

	if err := d.dumpSchemas(w, dbName, included); err != nil {
		return err
	}
	if err := d.dumpExtensions(w, dbName); err != nil {
		return err
	}
	if err := d.dumpTypes(w, dbName); err != nil {
		return err
	}

	if err := d.dumpSequences(w, dbName); err != nil {
		return err
	}

	postTables := append([]string{}, included...)
	for _, table := range included {
		common.TraceTable(d.ctxOrBg(), dbName, table)
		partitionNames, err := d.dumpTable(w, dbName, table)
		if err != nil {
			return err
		}
		postTables = append(postTables, partitionNames...)
	}

	if err := d.dumpViews(w, dbName); err != nil {
		return err
	}
	if err := d.dumpFunctions(w, dbName); err != nil {
		return err
	}

	if err := d.dumpTriggers(w, dbName, postTables); err != nil {
		return err
	}
	if err := d.dumpBlobs(w, dbName, postTables); err != nil {
		return err
	}
	if err := d.dumpSequenceValues(w, dbName); err != nil {
		return err
	}
	if err := d.dumpACLs(w, dbName, postTables); err != nil {
		return err
	}
	log.Debug("postgres", "database done",
		"database", dbName, "tables", len(tables),
		"elapsed", time.Since(dbStart).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dumpFunctions(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT proname, pg_get_functiondef(oid) FROM pg_proc "+
			"WHERE pronamespace NOT IN ('pg_catalog'::regnamespace, 'information_schema'::regnamespace) "+
			"AND prokind IN ('f', 'p') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_depend dd WHERE dd.objid = pg_proc.oid AND dd.deptype = 'e')")
	if err != nil {
		return fmt.Errorf("query functions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return fmt.Errorf("scan function row: %w", err)
		}
		def = strings.TrimSuffix(strings.TrimSpace(def), ";") + ";"
		log.Trace("postgres", "function dumped", "database", dbName, "function", name)
		fmt.Fprintf(w, "\n-- Function: %s\n%s\n\n", name, def)
	}
	return rows.Err()
}

func (d *Dumper) listUserSequences() ([][3]string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.relname, pg_get_userbyid(c.relowner) "+
			"FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE c.relkind = 'S' "+
			"AND n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND n.nspname NOT LIKE 'pg\\_%' "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, c.relname")
	if err != nil {
		return nil, fmt.Errorf("list sequences: %w", err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var s [3]string
		if err := rows.Scan(&s[0], &s[1], &s[2]); err != nil {
			return nil, fmt.Errorf("scan sequence row: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *Dumper) dumpDrops(w io.Writer, dbName string, included []string) error {
	fmt.Fprintf(w, "\n-- Drops (re-restore safety)\n")
	for _, t := range included {
		parts := strings.SplitN(t, ".", 2)
		if len(parts) != 2 {
			continue
		}
		log.Trace("postgres", "table dropped", "database", dbName, "table", t)
		fmt.Fprintf(w, "DROP TABLE IF EXISTS %s.%s CASCADE;\n",
			quotePGIdent(parts[0]), quotePGIdent(parts[1]))
	}
	if seqs, err := d.listUserSequences(); err == nil {
		for _, s := range seqs {
			fmt.Fprintf(w, "DROP SEQUENCE IF EXISTS %s.%s CASCADE;\n",
				quotePGIdent(s[0]), quotePGIdent(s[1]))
		}
	} else {
		log.Trace("postgres", "sequence drops unavailable", "database", dbName, "error", err.Error())
	}
	if types, err := d.listUserTypes(); err == nil {
		for _, t := range types {
			kw := "TYPE"
			if t.kind == "d" {
				kw = "DOMAIN"
			}
			fmt.Fprintf(w, "DROP %s IF EXISTS %s.%s CASCADE;\n", kw,
				quotePGIdent(t.schema), quotePGIdent(t.name))
		}
	} else {
		log.Trace("postgres", "type drops unavailable", "database", dbName, "error", err.Error())
	}
	return nil
}

type userTypeRef struct{ schema, name, kind string }

func (d *Dumper) listUserTypes() ([]userTypeRef, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, t.typname, t.typtype "+
			"FROM pg_catalog.pg_type t "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND n.nspname NOT LIKE 'pg\\_%' "+
			"AND t.typtype IN ('e', 'c', 'd', 'r', 'm') "+
			"AND (t.typrelid = 0 OR (SELECT c.relkind FROM pg_catalog.pg_class c WHERE c.oid = t.typrelid) = 'c') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = t.oid AND dd.deptype = 'e') "+
			"ORDER BY t.oid")
	if err != nil {
		return nil, fmt.Errorf("list types: %w", err)
	}
	defer rows.Close()
	var out []userTypeRef
	for rows.Next() {
		var r userTypeRef
		if err := rows.Scan(&r.schema, &r.name, &r.kind); err != nil {
			return nil, fmt.Errorf("scan type row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *Dumper) dumpSequences(w io.Writer, dbName string) error {
	start := time.Now()
	seqs, err := d.listUserSequences()
	if err != nil {
		return err
	}
	for _, s := range seqs {
		schema, name, owner := s[0], s[1], s[2]
		qn := quotePGIdent(schema) + "." + quotePGIdent(name)
		var opts string
		err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT 'START ' || ps.seqstart || ' INCREMENT BY ' || ps.seqincrement || "+
				"' MINVALUE ' || ps.seqmin || ' MAXVALUE ' || ps.seqmax || ' CACHE ' || ps.seqcache || "+
				"CASE WHEN ps.seqcycle THEN ' CYCLE' ELSE '' END "+
				"FROM pg_catalog.pg_sequence ps "+
				"JOIN pg_catalog.pg_class c ON c.oid = ps.seqrelid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2", schema, name).Scan(&opts)
		if err != nil {
			log.Trace("postgres", "sequence skipped", "database", dbName,
				"sequence", schema+"."+name, "error", err.Error())
			continue
		}
		log.Trace("postgres", "sequence dumped", "database", dbName,
			"sequence", schema+"."+name)
		fmt.Fprintf(w, "\n-- Sequence: %s.%s\n", schema, name)
		fmt.Fprintf(w, "CREATE SEQUENCE IF NOT EXISTS %s %s;\n", qn, opts)
		if owner != "" {
			fmt.Fprintf(w, "ALTER SEQUENCE %s OWNER TO %s;\n", qn, quotePGIdent(owner))
		}
	}
	log.Debug("postgres", "sequences done", "database", dbName,
		"count", len(seqs), "elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dumpSequenceValues(w io.Writer, dbName string) error {
	if d.SchemaOnly {
		return nil
	}
	seqs, err := d.listUserSequences()
	if err != nil {
		return err
	}
	wrote := false
	link := func() {
		if !wrote {
			fmt.Fprintf(w, "\n-- Sequence ownership and values\n")
			wrote = true
		}
	}
	for _, s := range seqs {
		schema, name := s[0], s[1]
		qn := quotePGIdent(schema) + "." + quotePGIdent(name)
		var ownTable, ownCol string
		if err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT ns.nspname || '.' || c.relname, a.attname "+
				"FROM pg_catalog.pg_depend dd "+
				"JOIN pg_catalog.pg_class s ON s.oid = dd.objid "+
				"JOIN pg_catalog.pg_namespace sn ON sn.oid = s.relnamespace "+
				"JOIN pg_catalog.pg_class c ON c.oid = dd.refobjid "+
				"JOIN pg_catalog.pg_namespace ns ON ns.oid = c.relnamespace "+
				"JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid AND a.attnum = dd.refobjsubid "+
				"WHERE sn.nspname = $1 AND s.relname = $2 AND dd.deptype = 'a' AND dd.refobjsubid > 0 "+
				"LIMIT 1", schema, name).Scan(&ownTable, &ownCol); err == nil && ownTable != "" {
			oparts := strings.SplitN(ownTable, ".", 2)
			link()
			fmt.Fprintf(w, "ALTER SEQUENCE %s OWNED BY %s.%s;\n", qn,
				quotePGIdent(oparts[0]), quotePGIdent(oparts[1])+"."+quotePGIdent(ownCol))
		}
		var lastVal int64
		var isCalled bool
		if err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT last_value, is_called FROM "+qn).Scan(&lastVal, &isCalled); err != nil {
			log.Trace("postgres", "sequence value unreadable", "database", dbName,
				"sequence", schema+"."+name, "error", err.Error())
			continue
		}
		link()
		called := "false"
		if isCalled {
			called = "true"
		}
		fmt.Fprintf(w, "SELECT pg_catalog.setval('%s', %d, %s);\n",
			escapePGLiteral(schema+"."+name), lastVal, called)
		log.Trace("postgres", "sequence value dumped", "database", dbName,
			"sequence", schema+"."+name, "last_value", lastVal, "is_called", isCalled)
	}
	return nil
}

func (d *Dumper) dumpTable(w io.Writer, dbName, table string) ([]string, error) {
	parts := strings.SplitN(table, ".", 2)
	schema := parts[0]
	tableName := parts[1]

	createSQL, err := d.getCreateTable(schema, tableName)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(w, "\n-- Table: %s.%s\n%s\n\n", schema, tableName, createSQL)
	if owner, err := d.ownerOf(schema, tableName); err == nil && owner != "" {
		fmt.Fprintf(w, "ALTER TABLE %s.%s OWNER TO %s;\n\n",
			quotePGIdent(schema), quotePGIdent(tableName), quotePGIdent(owner))
	}

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

	if d.isPartitioned(schema, tableName) {
		log.Trace("postgres", "partitioned parent, DDL only", "database", dbName, "table", table)
		return d.dumpPartitions(w, dbName, schema, tableName, schemaOnly, 0)
	}
	if schemaOnly {
		return nil, nil
	}

	if err := d.copyData(w, dbName, schema, tableName); err != nil {
		return nil, err
	}

	return nil, nil
}

func (d *Dumper) isPartitioned(schema, table string) bool {
	var kind string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT c.relkind FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2", schema, table).Scan(&kind)
	return err == nil && kind == "p"
}

func (d *Dumper) dumpPartitions(w io.Writer, dbName, schema, table string, schemaOnly bool, depth int) ([]string, error) {
	if depth > 8 {
		return nil, fmt.Errorf("partition nesting too deep at %s.%s", schema, table)
	}
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.relname, pg_get_expr(c.relpartbound, c.oid) "+
			"FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"JOIN pg_catalog.pg_inherits i ON i.inhrelid = c.oid "+
			"JOIN pg_catalog.pg_class p ON p.oid = i.inhparent "+
			"JOIN pg_catalog.pg_namespace pn ON pn.oid = p.relnamespace "+
			"WHERE pn.nspname = $1 AND p.relname = $2 ORDER BY c.relname", schema, table)
	if err != nil {
		return nil, fmt.Errorf("list partitions: %w", err)
	}
	type partRef struct{ schema, name, bound string }
	var parts []partRef
	for rows.Next() {
		var p partRef
		if err := rows.Scan(&p.schema, &p.name, &p.bound); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan partition row: %w", err)
		}
		parts = append(parts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var dumped []string
	for _, p := range parts {
		pschema, pname, bound := p.schema, p.name, p.bound
		qualified := pschema + "." + pname
		dumped = append(dumped, qualified)
		common.TraceTable(d.ctxOrBg(), dbName, qualified)
		log.Trace("postgres", "partition dumped", "database", dbName,
			"table", qualified, "parent", schema+"."+table)
		fmt.Fprintf(w, "\n-- Partition: %s (of %s.%s)\n", qualified, schema, table)
		fmt.Fprintf(w, "CREATE TABLE %s PARTITION OF %s.%s %s;\n",
			quotePGIdent(pschema)+"."+quotePGIdent(pname),
			quotePGIdent(schema), quotePGIdent(table), bound)
		if owner, err := d.ownerOf(pschema, pname); err == nil && owner != "" {
			fmt.Fprintf(w, "ALTER TABLE %s.%s OWNER TO %s;\n",
				quotePGIdent(pschema), quotePGIdent(pname), quotePGIdent(owner))
		}
		fmt.Fprintf(w, "\n")
		if schemaOnly {
			continue
		}
		if err := d.copyData(w, dbName, pschema, pname); err != nil {
			return dumped, err
		}
		if d.isPartitioned(pschema, pname) {
			nested, err := d.dumpPartitions(w, dbName, pschema, pname, schemaOnly, depth+1)
			if err != nil {
				return dumped, err
			}
			dumped = append(dumped, nested...)
		}
	}
	return dumped, nil
}

func (d *Dumper) dumpViews(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT table_schema, table_name, view_definition FROM information_schema.views "+
			"WHERE table_schema NOT IN ('pg_catalog', 'information_schema')")
	if err != nil {
		return fmt.Errorf("query views: %w", err)
	}
	defer rows.Close()

	extMembers, _ := d.extensionMembers()

	for rows.Next() {
		var schema, name, def string
		if err := rows.Scan(&schema, &name, &def); err != nil {
			return fmt.Errorf("scan view row: %w", err)
		}
		if def == "" {
			continue
		}
		if extMembers[schema+"."+name] {
			log.Trace("postgres", "skipped extension member", "database", dbName,
				"view", schema+"."+name)
			continue
		}
		fmt.Fprintf(w, "\n-- View: %s.%s\n", schema, name)
		fmt.Fprintf(w, "CREATE OR REPLACE VIEW %s.%s AS\n%s;\n", schema, name, def)
		if owner, err := d.ownerOf(schema, name); err == nil && owner != "" {
			fmt.Fprintf(w, "ALTER VIEW %s.%s OWNER TO %s;\n",
				quotePGIdent(schema), quotePGIdent(name), quotePGIdent(owner))
		}
		fmt.Fprintf(w, "\n")
	}
	return rows.Err()
}

func escapePGLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

const (
	pgOIDOIDBytea = 17
	pgOIDOIDJSON  = 114
	pgOIDOIDUUID  = 2950
	pgOIDOIDJSONB = 3802
)

func encodeCopyValue(val any, oid uint32) (string, bool) {
	if oid == pgOIDOIDJSON || oid == pgOIDOIDJSONB {
		if val == nil {
			return "\\N", false
		}
		if s, ok := val.(string); ok {
			return escapePgCopy(s), false
		}
		if b, ok := val.([]byte); ok {
			return escapePgCopy(string(b)), false
		}
		if raw, err := json.Marshal(val); err == nil {
			return escapePgCopy(string(raw)), false
		}
		return escapePgCopy(fmt.Sprintf("%v", val)), true
	}
	if oid == pgOIDOIDUUID {
		if val == nil {
			return "\\N", false
		}
		if s, ok := val.(string); ok {
			return escapePgCopy(s), false
		}
		if b, ok := val.([]byte); ok && len(b) == 16 {
			return encodeCopyUUID(b), false
		}
		rv := reflect.ValueOf(val)
		if (rv.Kind() == reflect.Array || rv.Kind() == reflect.Slice) && rv.Len() == 16 {
			if k := rv.Type().Elem().Kind(); k == reflect.Uint8 {
				b := make([]byte, 16)
				reflect.Copy(reflect.ValueOf(b), rv)
				return encodeCopyUUID(b), false
			}
		}
	}
	switch v := val.(type) {
	case nil:
		return "\\N", false
	case string:
		return escapePgCopy(v), false
	case []byte:
		if oid == pgOIDOIDJSON || oid == pgOIDOIDJSONB {
			return escapePgCopy(string(v)), false
		}
		return "\\\\x" + hex.EncodeToString(v), false
	case bool:
		if v {
			return "t", false
		}
		return "f", false
	case int16:
		return strconv.FormatInt(int64(v), 10), false
	case int32:
		return strconv.FormatInt(int64(v), 10), false
	case int64:
		return strconv.FormatInt(v, 10), false
	case uint16:
		return strconv.FormatUint(uint64(v), 10), false
	case uint32:
		return strconv.FormatUint(uint64(v), 10), false
	case uint64:
		return strconv.FormatUint(v, 10), false
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32), false
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), false
	case time.Time:
		return v.Format("2006-01-02 15:04:05.999999-07"), false
	case pgtype.Interval:
		if !v.Valid {
			return "\\N", false
		}
		return encodeCopyInterval(v), false
	case pgtype.Numeric:
		if !v.Valid {
			return "\\N", false
		}
		return formatCopyNumeric(v), false
	case fmt.Stringer:
		return escapePgCopy(v.String()), false
	}
	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		return encodeCopyArray(rv), false
	case reflect.Map:
		return encodeCopyHstore(rv), false
	case reflect.Ptr:
		if rv.IsNil() {
			return "\\N", false
		}
		return encodeCopyValue(rv.Elem().Interface(), oid)
	}
	return escapePgCopy(fmt.Sprintf("%v", val)), true
}

func encodeCopyUUID(b []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func encodeCopyArray(rv reflect.Value) string {
	elems := make([]string, rv.Len())
	for i := range elems {
		ev := rv.Index(i).Interface()
		if ev == nil {
			elems[i] = "NULL"
			continue
		}
		er := reflect.ValueOf(ev)
		if er.Kind() == reflect.Ptr && er.IsNil() {
			elems[i] = "NULL"
			continue
		}
		if er.Kind() == reflect.Slice || er.Kind() == reflect.Array {
			elems[i] = encodeCopyArray(er)
			continue
		}
		s, _ := encodeCopyValue(ev, 0)
		if needsArrayQuoting(s, ev) {
			elems[i] = `"` + strings.ReplaceAll(s, `"`, `\\"`) + `"`
		} else {
			elems[i] = s
		}
	}
	return "{" + strings.Join(elems, ",") + "}"
}

func needsArrayQuoting(s string, ev any) bool {
	if _, ok := ev.(string); ok {
		return true
	}
	if s == "" || s == "NULL" {
		return true
	}
	return strings.ContainsAny(s, "{},\"\\ \t\n\r")
}

func encodeCopyHstore(rv reflect.Value) string {
	pairs := make([]string, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k := fmt.Sprintf("%v", iter.Key().Interface())
		vv := iter.Value().Interface()
		if vv == nil {
			pairs = append(pairs, `"`+strings.ReplaceAll(k, `"`, `\"`)+`" => NULL`)
			continue
		}
		vr := reflect.ValueOf(vv)
		if vr.Kind() == reflect.Ptr {
			if vr.IsNil() {
				pairs = append(pairs, `"`+strings.ReplaceAll(k, `"`, `\"`)+`" => NULL`)
				continue
			}
			vv = vr.Elem().Interface()
		}
		s, _ := encodeCopyValue(vv, 0)
		pairs = append(pairs, `"`+strings.ReplaceAll(k, `"`, `\"`)+`"=>"`+
			strings.ReplaceAll(s, `"`, `\"`)+`"`)
	}
	sort.Strings(pairs)
	return escapePgCopy(strings.Join(pairs, ", "))
}

func encodeCopyInterval(v pgtype.Interval) string {
	var parts []string
	if v.Months != 0 {
		years, mons := v.Months/12, v.Months%12
		if years != 0 {
			parts = append(parts, fmt.Sprintf("%d year%s", years, plural(years)))
		}
		if mons != 0 {
			parts = append(parts, fmt.Sprintf("%d mon%s", mons, plural(mons)))
		}
	}
	if v.Days != 0 {
		parts = append(parts, fmt.Sprintf("%d day%s", v.Days, plural(v.Days)))
	}
	us := v.Microseconds
	if us != 0 || len(parts) == 0 {
		neg := us < 0
		if neg {
			us = -us
		}
		h, us := us/3600000000, us%3600000000
		m, us := us/60000000, us%60000000
		sec, frac := us/1000000, us%1000000
		ts := fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
		if frac != 0 {
			ts += strings.TrimRight(fmt.Sprintf(".%06d", frac), "0")
		}
		if neg {
			ts = "-" + ts
		}
		parts = append(parts, ts)
	}
	return strings.Join(parts, " ")
}

func formatCopyNumeric(v pgtype.Numeric) string {
	if v.NaN {
		return "NaN"
	}
	switch v.InfinityModifier {
	case pgtype.Infinity:
		return "Infinity"
	case pgtype.NegativeInfinity:
		return "-Infinity"
	}
	if v.Int == nil {
		return "0"
	}
	dig := v.Int.String()
	neg := strings.HasPrefix(dig, "-")
	if neg {
		dig = dig[1:]
	}
	var out string
	if v.Exp >= 0 {
		out = dig + strings.Repeat("0", int(v.Exp))
	} else if k := int(-v.Exp); k >= len(dig) {
		out = "0." + strings.Repeat("0", k-len(dig)) + dig
	} else {
		out = dig[:len(dig)-k] + "." + dig[len(dig)-k:]
	}
	if neg {
		out = "-" + out
	}
	return out
}

func plural(n int32) string {
	if n == 1 || n == -1 {
		return ""
	}
	return "s"
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
		"SELECT t.table_schema, t.table_name FROM information_schema.tables t "+
			"WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema') "+
			"AND t.table_type = 'BASE TABLE' "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c "+
			"  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"  WHERE n.nspname = t.table_schema AND c.relname = t.table_name "+
			"  AND c.relispartition) "+
			"ORDER BY t.table_schema, t.table_name")
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	extMembers, _ := d.extensionMembers()

	for rows.Next() {
		var schema, table string
		rows.Scan(&schema, &table)
		if extMembers[schema+"."+table] {
			log.Trace("postgres", "skipped extension member", "database", dbName,
				"table", schema+"."+table)
			continue
		}
		tables = append(tables, schema+"."+table)
	}
	return tables, rows.Err()
}

func (d *Dumper) extensionMembers() (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.relname FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"JOIN pg_catalog.pg_depend dd ON dd.objid = c.oid AND dd.deptype = 'e'")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return out, err
		}
		out[schema+"."+name] = true
	}
	return out, rows.Err()
}

type pgSchemaInfo struct {
	name  string
	owner string
}

func (d *Dumper) listSchemas() ([]pgSchemaInfo, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, pg_get_userbyid(n.nspowner) FROM pg_catalog.pg_namespace n "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND n.nspname NOT LIKE 'pg\\_%' ORDER BY n.nspname")
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}
	defer rows.Close()
	var out []pgSchemaInfo
	for rows.Next() {
		var s pgSchemaInfo
		if err := rows.Scan(&s.name, &s.owner); err != nil {
			return nil, fmt.Errorf("scan schema row: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *Dumper) dumpSchemas(w io.Writer, dbName string, included []string) error {
	schemas, err := d.listSchemas()
	if err != nil {
		return err
	}
	needed := map[string]bool{}
	if d.Tables == nil {
		for _, s := range schemas {
			needed[s.name] = true
		}
	} else {
		for _, t := range included {
			if parts := strings.SplitN(t, ".", 2); len(parts) == 2 {
				needed[parts[0]] = true
			}
		}
	}
	for _, s := range schemas {
		if !needed[s.name] {
			log.Trace("postgres", "schema skipped by filter", "database", dbName, "schema", s.name)
			continue
		}
		log.Trace("postgres", "schema dumped", "database", dbName, "schema", s.name, "owner", s.owner)
		fmt.Fprintf(w, "\n-- Schema: %s\n", s.name)
		fmt.Fprintf(w, "CREATE SCHEMA IF NOT EXISTS %s;\n", quotePGIdent(s.name))
		if s.owner != "" {
			fmt.Fprintf(w, "ALTER SCHEMA %s OWNER TO %s;\n",
				quotePGIdent(s.name), quotePGIdent(s.owner))
		}
		if grants, err := d.schemaGrants(s.name); err != nil {
			log.Trace("postgres", "schema grants unavailable", "database", dbName,
				"schema", s.name, "error", err.Error())
		} else if len(grants) > 0 {
			fmt.Fprint(w, formatGrants("SCHEMA "+quotePGIdent(s.name), grants))
		}
	}
	return nil
}

func (d *Dumper) dumpExtensions(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT e.extname, n.nspname FROM pg_catalog.pg_extension e "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = e.extnamespace ORDER BY e.extname")
	if err != nil {
		return fmt.Errorf("list extensions: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var ext, schema string
		if err := rows.Scan(&ext, &schema); err != nil {
			return fmt.Errorf("scan extension row: %w", err)
		}
		names = append(names, ext)
		fmt.Fprintf(w, "\n-- Extension: %s\n", ext)
		fmt.Fprintf(w, "CREATE EXTENSION IF NOT EXISTS %s WITH SCHEMA %s;\n",
			quotePGIdent(ext), quotePGIdent(schema))
		log.Trace("postgres", "extension dumped", "database", dbName, "extension", ext, "schema", schema)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	log.Debug("postgres", "extensions done", "database", dbName,
		"count", len(names), "extensions", strings.Join(names, ","))
	return nil
}

func (d *Dumper) ownerOf(schema, name string) (string, error) {
	var owner string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT pg_get_userbyid(c.relowner) FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2", schema, name).Scan(&owner)
	return owner, err
}

func (d *Dumper) dumpTypes(w io.Writer, dbName string) error {
	start := time.Now()
	refs, err := d.listUserTypes()
	if err != nil {
		return err
	}

	count := 0
	for _, r := range refs {
		schema, name, kind := r.schema, r.name, r.kind
		owner, _ := d.typeOwner(schema, name)
		def, err := d.typeDef(schema, name, kind)
		if err != nil {
			log.Trace("postgres", "type skipped", "database", dbName,
				"type", schema+"."+name, "error", err.Error())
			continue
		}
		if def == "" {
			continue
		}
		count++
		log.Trace("postgres", "type dumped", "database", dbName,
			"type", schema+"."+name, "kind", kind)
		fmt.Fprintf(w, "\n-- Type: %s.%s\n%s\n", schema, name, def)
		if owner != "" {
			kw := "TYPE"
			if kind == "d" {
				kw = "DOMAIN"
			}
			fmt.Fprintf(w, "ALTER %s %s.%s OWNER TO %s;\n", kw,
				quotePGIdent(schema), quotePGIdent(name), quotePGIdent(owner))
		}
		if grants, err := d.typeGrants(schema, name); err != nil {
			log.Trace("postgres", "grants unavailable", "database", dbName,
				"object", "TYPE "+schema+"."+name, "error", err.Error())
		} else if len(grants) > 0 {
			fmt.Fprint(w, formatGrants(
				"TYPE "+quotePGIdent(schema)+"."+quotePGIdent(name), grants))
		}
	}
	log.Debug("postgres", "types done", "database", dbName, "count", count,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) typeOwner(schema, name string) (string, error) {
	var owner string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT pg_get_userbyid(t.typowner) FROM pg_catalog.pg_type t "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
			"WHERE n.nspname = $1 AND t.typname = $2", schema, name).Scan(&owner)
	return owner, err
}

func (d *Dumper) typeDef(schema, name, kind string) (string, error) {
	qn := quotePGIdent(schema) + "." + quotePGIdent(name)
	ctx := d.ctxOrBg()
	switch kind {
	case "e":
		rows, err := d.conn.Query(ctx,
			"SELECT enumlabel FROM pg_catalog.pg_enum e "+
				"JOIN pg_catalog.pg_type t ON t.oid = e.enumtypid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
				"WHERE n.nspname = $1 AND t.typname = $2 ORDER BY e.enumsortorder",
			schema, name)
		if err != nil {
			return "", err
		}
		var labels []string
		for rows.Next() {
			var l string
			if err := rows.Scan(&l); err != nil {
				rows.Close()
				return "", err
			}
			labels = append(labels, "'"+escapePGLiteral(l)+"'")
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		return fmt.Sprintf("DROP TYPE IF EXISTS %s;\nCREATE TYPE %s AS ENUM (%s);",
			qn, qn, strings.Join(labels, ", ")), nil
	case "c":
		rows, err := d.conn.Query(ctx,
			"SELECT a.attname, format_type(a.atttypid, a.atttypmod) "+
				"FROM pg_catalog.pg_attribute a "+
				"JOIN pg_catalog.pg_class c ON c.oid = a.attrelid "+
				"JOIN pg_catalog.pg_type t ON t.typrelid = c.oid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
				"WHERE n.nspname = $1 AND t.typname = $2 "+
				"AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum",
			schema, name)
		if err != nil {
			return "", err
		}
		var attrs []string
		for rows.Next() {
			var col, typ string
			if err := rows.Scan(&col, &typ); err != nil {
				rows.Close()
				return "", err
			}
			attrs = append(attrs, quotePGIdent(col)+" "+typ)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		return fmt.Sprintf("DROP TYPE IF EXISTS %s;\nCREATE TYPE %s AS (%s);",
			qn, qn, strings.Join(attrs, ", ")), nil
	case "d":
		var base string
		var notNull bool
		var defVal sql.NullString
		err := d.conn.QueryRow(ctx,
			"SELECT t.typbasetype::regtype::text, t.typnotnull, t.typdefault "+
				"FROM pg_catalog.pg_type t "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
				"WHERE n.nspname = $1 AND t.typname = $2", schema, name).
			Scan(&base, &notNull, &defVal)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "DROP DOMAIN IF EXISTS %s;\nCREATE DOMAIN %s AS %s", qn, qn, base)
		if defVal.Valid && defVal.String != "" {
			sb.WriteString(" DEFAULT " + defVal.String)
		}
		if notNull {
			sb.WriteString(" NOT NULL")
		}
		sb.WriteString(";")
		rows, err := d.conn.Query(ctx,
			"SELECT pg_get_constraintdef(c.oid) FROM pg_catalog.pg_constraint c "+
				"JOIN pg_catalog.pg_type t ON t.oid = c.contypid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
				"WHERE n.nspname = $1 AND t.typname = $2 ORDER BY c.conname",
			schema, name)
		if err == nil {
			for rows.Next() {
				var cdef string
				if err := rows.Scan(&cdef); err != nil {
					break
				}
				if cdef != "" {
					fmt.Fprintf(&sb, "\nALTER DOMAIN %s ADD %s;", qn, cdef)
				}
			}
			rows.Close()
		}
		return sb.String(), nil
	case "r":
		var subtype, canonical, diff string
		err := d.conn.QueryRow(ctx,
			"SELECT r.rngsubtype::regtype::text, "+
				"COALESCE(r.rngcanonical::regprocedure::text, ''), "+
				"COALESCE(r.rngsubdiff::regprocedure::text, '') "+
				"FROM pg_catalog.pg_range r "+
				"JOIN pg_catalog.pg_type t ON t.oid = r.rngtypid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
				"WHERE n.nspname = $1 AND t.typname = $2", schema, name).
			Scan(&subtype, &canonical, &diff)
		if err != nil {
			return "", err
		}
		opts := "subtype = " + subtype
		if canonical != "" && canonical != "-" {
			opts += ", canonical = " + canonical
		}
		if diff != "" && diff != "-" {
			opts += ", subtype_diff = " + diff
		}
		return fmt.Sprintf("DROP TYPE IF EXISTS %s;\nCREATE TYPE %s AS RANGE (%s);",
			qn, qn, opts), nil
	case "m":
		var rng string
		err := d.conn.QueryRow(ctx,
			"SELECT r.rngtypid::regtype::text FROM pg_catalog.pg_range r "+
				"JOIN pg_catalog.pg_type t ON t.oid = r.rngmultitypid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
				"WHERE n.nspname = $1 AND t.typname = $2", schema, name).
			Scan(&rng)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("DROP TYPE IF EXISTS %s;\nCREATE TYPE %s AS MULTIRANGE (multirange_range_name = %s);",
			qn, qn, rng), nil
	}
	return "", fmt.Errorf("unknown type kind %q", kind)
}

func (d *Dumper) typeGrants(schema, name string) ([]pgGrant, error) {
	return d.aclGrants(
		"SELECT CASE WHEN x.grantee = 0 THEN 'PUBLIC' ELSE x.grantee::regrole::text END, "+
			"x.privilege_type, x.is_grantable "+
			"FROM pg_catalog.pg_type t "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
			"CROSS JOIN LATERAL aclexplode(t.typacl) x "+
			"WHERE n.nspname = $1 AND t.typname = $2 ORDER BY 1, 2", schema, name)
}

type pgGrant struct {
	grantee   string
	priv      string
	grantable bool
}

func (d *Dumper) relationGrants(schema, name string) ([]pgGrant, error) {
	return d.aclGrants(
		"SELECT CASE WHEN x.grantee = 0 THEN 'PUBLIC' ELSE x.grantee::regrole::text END, "+
			"x.privilege_type, x.is_grantable "+
			"FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"CROSS JOIN LATERAL aclexplode(c.relacl) x "+
			"WHERE n.nspname = $1 AND c.relname = $2 ORDER BY 1, 2", schema, name)
}

func (d *Dumper) schemaGrants(schema string) ([]pgGrant, error) {
	return d.aclGrants(
		"SELECT CASE WHEN x.grantee = 0 THEN 'PUBLIC' ELSE x.grantee::regrole::text END, "+
			"x.privilege_type, x.is_grantable "+
			"FROM pg_catalog.pg_namespace n "+
			"CROSS JOIN LATERAL aclexplode(n.nspacl) x "+
			"WHERE n.nspname = $1 ORDER BY 1, 2", schema)
}

func (d *Dumper) aclGrants(query, schema string, args ...any) ([]pgGrant, error) {
	rows, err := d.conn.Query(d.ctxOrBg(), query, append([]any{schema}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgGrant
	for rows.Next() {
		var g pgGrant
		if err := rows.Scan(&g.grantee, &g.priv, &g.grantable); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func formatGrants(target string, grants []pgGrant) string {
	type key struct {
		grantee   string
		grantable bool
	}
	var order []key
	privs := map[key][]string{}
	for _, g := range grants {
		k := key{g.grantee, g.grantable}
		if _, ok := privs[k]; !ok {
			order = append(order, k)
		}
		privs[k] = append(privs[k], g.priv)
	}
	var sb strings.Builder
	for _, k := range order {
		to := k.grantee
		if to != "PUBLIC" {
			to = quotePGIdent(to)
		}
		line := fmt.Sprintf("GRANT %s ON %s TO %s",
			strings.Join(privs[k], ", "), target, to)
		if k.grantable {
			line += " WITH GRANT OPTION"
		}
		sb.WriteString(line + ";\n")
	}
	return sb.String()
}

func (d *Dumper) dumpTriggers(w io.Writer, dbName string, tables []string) error {
	start := time.Now()
	count := 0
	for _, t := range tables {
		parts := strings.SplitN(t, ".", 2)
		if len(parts) != 2 {
			continue
		}
		schema, table := parts[0], parts[1]
		rows, err := d.conn.Query(d.ctxOrBg(),
			"SELECT tg.tgname, pg_get_triggerdef(tg.oid) "+
				"FROM pg_catalog.pg_trigger tg "+
				"JOIN pg_catalog.pg_class c ON c.oid = tg.tgrelid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2 AND NOT tg.tgisinternal "+
				"ORDER BY tg.tgname", schema, table)
		if err != nil {
			log.Trace("postgres", "triggers unavailable", "database", dbName,
				"table", t, "error", err.Error())
			continue
		}
		for rows.Next() {
			var name, def string
			if err := rows.Scan(&name, &def); err != nil {
				break
			}
			if def == "" {
				continue
			}
			count++
			log.Trace("postgres", "trigger dumped", "database", dbName,
				"table", t, "trigger", name)
			fmt.Fprintf(w, "\n-- Trigger: %s (on %s.%s)\n", name, schema, table)
			fmt.Fprintf(w, "DROP TRIGGER IF EXISTS %s ON %s.%s;\n",
				quotePGIdent(name), quotePGIdent(schema), quotePGIdent(table))
			fmt.Fprintf(w, "%s;\n", strings.TrimSuffix(strings.TrimSpace(def), ";"))
		}
		rows.Close()
	}
	log.Debug("postgres", "triggers done", "database", dbName,
		"count", count, "elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dumpBlobs(w io.Writer, dbName string, tables []string) error {
	if d.SchemaOnly {
		return nil
	}
	start := time.Now()
	loCols := map[string][]string{}
	for _, t := range tables {
		parts := strings.SplitN(t, ".", 2)
		if len(parts) != 2 {
			continue
		}
		cols, err := d.loColumns(parts[0], parts[1])
		if err != nil {
			log.Trace("postgres", "lo columns unavailable", "database", dbName,
				"table", t, "error", err.Error())
			continue
		}
		if len(cols) > 0 {
			loCols[t] = cols
		}
	}
	if len(loCols) == 0 {
		return nil
	}

	oids := map[uint32]bool{}
	var ordered []uint32
	for t, cols := range loCols {
		parts := strings.SplitN(t, ".", 2)
		for _, c := range cols {
			rows, err := d.conn.Query(d.ctxOrBg(),
				"SELECT DISTINCT "+quotePGIdent(c)+" FROM "+
					quotePGIdent(parts[0])+"."+quotePGIdent(parts[1])+
					" WHERE "+quotePGIdent(c)+" IS NOT NULL")
			if err != nil {
				log.Trace("postgres", "lo oids unreadable", "database", dbName,
					"table", t, "column", c, "error", err.Error())
				continue
			}
			for rows.Next() {
				var oid uint32
				if err := rows.Scan(&oid); err != nil {
					continue
				}
				if !oids[oid] {
					oids[oid] = true
					ordered = append(ordered, oid)
				}
			}
			rows.Close()
		}
	}
	if len(ordered) == 0 {
		return nil
	}

	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	fmt.Fprintf(w, "\n-- Large objects\n")
	for _, oid := range ordered {
		fmt.Fprintf(w, "DO $$BEGIN IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_largeobject_metadata WHERE oid = %d) THEN PERFORM pg_catalog.lo_create(%d); END IF; END$$;\n", oid, oid)
		fmt.Fprintf(w, "DELETE FROM pg_catalog.pg_largeobject WHERE loid = %d;\n", oid)
	}
	idList := make([]string, len(ordered))
	for i, oid := range ordered {
		idList[i] = strconv.FormatUint(uint64(oid), 10)
	}
	fmt.Fprintf(w, "COPY pg_catalog.pg_largeobject (loid, pageno, data) FROM stdin;\n")
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT loid, pageno, data FROM pg_catalog.pg_largeobject WHERE loid IN ("+
			strings.Join(idList, ", ")+") ORDER BY loid, pageno")
	if err != nil {
		return fmt.Errorf("read large objects: %w", err)
	}
	pages := 0
	for rows.Next() {
		var loid, pageno uint32
		var data []byte
		if err := rows.Scan(&loid, &pageno, &data); err != nil {
			rows.Close()
			return fmt.Errorf("scan large object: %w", err)
		}
		fmt.Fprintf(w, "%d\t%d\t\\\\x%s\n", loid, pageno, hex.EncodeToString(data))
		pages++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\\.\n\n")
	log.Debug("postgres", "large objects done", "database", dbName,
		"objects", len(ordered), "pages", pages,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) loColumns(schema, table string) ([]string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT a.attname FROM pg_catalog.pg_attribute a "+
			"JOIN pg_catalog.pg_class c ON c.oid = a.attrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped "+
			"AND a.atttypid = 'oid'::regtype ORDER BY a.attnum", schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *Dumper) dumpACLs(w io.Writer, dbName string, tables []string) error {
	wroteHeader := false
	grantCount := 0
	header := func() {
		if !wroteHeader {
			fmt.Fprintf(w, "\n-- Post-data ACLs (permissions)\n")
			wroteHeader = true
		}
	}

	grantRelation := func(kind, schema, name string) {
		grants, err := d.relationGrants(schema, name)
		if err != nil {
			log.Trace("postgres", "grants unavailable", "database", dbName,
				"object", kind+" "+schema+"."+name, "error", err.Error())
			return
		}
		if len(grants) == 0 {
			log.Trace("postgres", "no explicit grants", "database", dbName,
				"object", kind+" "+schema+"."+name)
			return
		}
		grantCount += len(grants)
		log.Trace("postgres", "grants dumped", "database", dbName,
			"object", kind+" "+schema+"."+name, "grants", len(grants))
		header()
		fmt.Fprintf(w, "\n-- ACL: %s %s.%s\n", kind, schema, name)
		fmt.Fprint(w, formatGrants(
			"TABLE "+quotePGIdent(schema)+"."+quotePGIdent(name), grants))
	}

	for _, t := range tables {
		parts := strings.SplitN(t, ".", 2)
		if len(parts) != 2 {
			continue
		}
		grantRelation("TABLE", parts[0], parts[1])
	}

	viewRows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT table_schema, table_name FROM information_schema.views "+
			"WHERE table_schema NOT IN ('pg_catalog', 'information_schema')")
	if err == nil {
		extMembers, _ := d.extensionMembers()
		type viewRef struct{ schema, name string }
		var views []viewRef
		for viewRows.Next() {
			var v viewRef
			if err := viewRows.Scan(&v.schema, &v.name); err != nil {
				break
			}
			if extMembers[v.schema+"."+v.name] {
				continue
			}
			views = append(views, v)
		}
		viewRows.Close()
		for _, v := range views {
			grantRelation("VIEW", v.schema, v.name)
		}
	}

	funcRows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, p.proname, pg_get_function_identity_arguments(p.oid), "+
			"pg_get_userbyid(p.proowner), p.proacl FROM pg_catalog.pg_proc p "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND p.prokind IN ('f', 'p') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = p.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, p.proname")
	if err != nil {
		return nil
	}
	type funcRef struct{ schema, name, args, owner, acl string }
	var funcs []funcRef
	for funcRows.Next() {
		var f funcRef
		var aclVal sql.NullString
		if err := funcRows.Scan(&f.schema, &f.name, &f.args, &f.owner, &aclVal); err != nil {
			continue
		}
		if aclVal.Valid {
			f.acl = aclVal.String
		}
		funcs = append(funcs, f)
	}
	funcRows.Close()
	if err := funcRows.Err(); err != nil {
		return err
	}
	for _, f := range funcs {
		schema, name, args, owner := f.schema, f.name, f.args, f.owner
		ident := quotePGIdent(schema) + "." + quotePGIdent(name) + "(" + args + ")"
		emitted := false
		if owner != "" {
			header()
			if !emitted {
				fmt.Fprintf(w, "\n-- ACL: FUNCTION %s.%s(%s)\n", schema, name, args)
				emitted = true
			}
			fmt.Fprintf(w, "ALTER FUNCTION %s OWNER TO %s;\n", ident, quotePGIdent(owner))
		}
		if f.acl != "" && f.acl != "{}" {
			grants, err := d.funcGrants(schema, name, args)
			if err != nil {
				log.Trace("postgres", "grants unavailable", "database", dbName,
					"object", "FUNCTION "+schema+"."+name, "error", err.Error())
			} else if len(grants) > 0 {
				grantCount += len(grants)
				log.Trace("postgres", "grants dumped", "database", dbName,
					"object", "FUNCTION "+schema+"."+name, "grants", len(grants))
				header()
				if !emitted {
					fmt.Fprintf(w, "\n-- ACL: FUNCTION %s.%s(%s)\n", schema, name, args)
				}
				fmt.Fprint(w, formatGrants("FUNCTION "+ident, grants))
			}
		}
	}
	log.Debug("postgres", "ACLs done", "database", dbName, "grants", grantCount)
	return nil
}

func (d *Dumper) funcGrants(schema, name, args string) ([]pgGrant, error) {
	return d.aclGrants(
		"SELECT CASE WHEN x.grantee = 0 THEN 'PUBLIC' ELSE x.grantee::regrole::text END, "+
			"x.privilege_type, x.is_grantable "+
			"FROM pg_catalog.pg_proc p "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace "+
			"CROSS JOIN LATERAL aclexplode(p.proacl) x "+
			"WHERE n.nspname = $1 AND p.proname = $2 "+
			"AND pg_get_function_identity_arguments(p.oid) = $3 ORDER BY 1, 2", schema, name, args)
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

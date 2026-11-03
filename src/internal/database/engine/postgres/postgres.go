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
	server     common.ServerVersion
	tlsCfg     *config.TLSConfig
	connCfg    *config.ConnectivityConfig
	ctx        context.Context
	SplitDB    bool
	Tables     *config.TableFilter
	SchemaOnly bool
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

func (d *Dumper) Close() error {
	if d.conn != nil {
		return d.conn.Close(context.Background())
	}
	return nil
}

func (d *Dumper) copyData(w io.Writer, dbName, schema, table string) error {
	generated := map[string]bool{}
	var orderedCols []string
	if crows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT a.attname, (a.attgenerated != '') AS isgen, a.atttypid::bigint, t.typcategory::text, coalesce(et.typcategory::text, '') AS elemcat FROM pg_catalog.pg_attribute a "+
			"JOIN pg_catalog.pg_class c ON c.oid = a.attrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"JOIN pg_catalog.pg_type t ON t.oid = a.atttypid "+
			"LEFT JOIN pg_catalog.pg_type et ON et.oid = t.typelem "+
			"WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped "+
			"ORDER BY a.attnum", schema, table); err == nil {
		for crows.Next() {
			var name, cat, elemcat string
			var isgen bool
			var typid int64
			if err := crows.Scan(&name, &isgen, &typid, &cat, &elemcat); err != nil {
				orderedCols = nil
				break
			}
			if isgen {
				generated[name] = true
			} else if pgCopyTextCast(uint32(typid), cat, elemcat) {
				orderedCols = append(orderedCols, quotePGIdent(name)+"::text")
			} else {
				orderedCols = append(orderedCols, quotePGIdent(name))
			}
		}
		crows.Close()
	}
	query := "SELECT * FROM ONLY " + quotePGIdent(schema) + "." + quotePGIdent(table)
	if len(orderedCols) > 0 {
		query = "SELECT " + strings.Join(orderedCols, ", ") + " FROM ONLY " + quotePGIdent(schema) + "." + quotePGIdent(table)
	}
	rows, err := d.conn.Query(d.ctxOrBg(), query, pgx.QueryResultFormats{pgx.TextFormatCode})
	if err != nil {
		return fmt.Errorf("select %s.%s: %w", schema, table, err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	colNames := make([]string, len(fields))
	for i, f := range fields {
		colNames[i] = string(f.Name)
	}

	rawOK := make([]bool, len(fields))
	for i, f := range fields {
		switch f.DataTypeOID {
		case pgOIDOIDJSON, pgOIDOIDJSONB, pgOIDOIDJSONArray, pgOIDOIDJSONBArray:
			rawOK[i] = true
		default:
			if dt, ok := d.conn.TypeMap().TypeForOID(f.DataTypeOID); ok &&
				strings.HasSuffix(dt.Name, "range") {
				rawOK[i] = true
			}
		}
	}

	quotedCols := make([]string, len(colNames))
	for i, c := range colNames {
		quotedCols[i] = quotePGIdent(c)
	}
	fmt.Fprintf(w, "COPY %s.%s (%s) FROM stdin;\n", quotePGIdent(schema), quotePGIdent(table), strings.Join(quotedCols, ", "))

	fallbackCols := map[string]bool{}
	defer func() {
		if len(fallbackCols) == 0 {
			return
		}
		names := make([]string, 0, len(fallbackCols))
		for n := range fallbackCols {
			names = append(names, n)
		}
		sort.Strings(names)
		cols := strings.Join(names, ",")
		log.Warn("postgres", "generic COPY formatting used - verify restored data", "database", dbName,
			"table", schema+"."+table, "columns", cols)
	}()

	var rowCount int
	for rows.Next() {
		raw := rows.RawValues()
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		line := make([]string, len(values))
		for i, val := range values {
			var oid uint32
			isText := true
			if i < len(fields) {
				oid = fields[i].DataTypeOID
				isText = fields[i].Format == 0
			}
			var rawVal []byte
			haveRaw := i < len(raw)
			if haveRaw {
				rawVal = raw[i]
			}
			if haveRaw && rawVal == nil {
				line[i] = "\\N"
				continue
			}
			if haveRaw && isText && i < len(rawOK) && rawOK[i] {
				line[i] = escapePgCopy(string(rawVal))
				continue
			}
			enc, fallback := encodeCopyValue(val, oid)
			if fallback {
				fallbackCols[colNames[i]] = true
			}
			line[i] = escapePgCopy(enc)
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
			log.Warn("postgres", "grants unavailable", "database", dbName,
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
		type viewRef struct{ schema, name string }
		var views []viewRef
		for viewRows.Next() {
			var v viewRef
			if err := viewRows.Scan(&v.schema, &v.name); err != nil {
				break
			}
			views = append(views, v)
		}
		viewRows.Close()
		extMembers, extErr := d.extensionMembers()
		if extErr != nil {
			log.Warn("postgres", "extension members unavailable", "database", dbName,
				"error", extErr.Error())
		}
		for _, v := range views {
			if extMembers[v.schema+"."+v.name] {
				continue
			}
			grantRelation("VIEW", v.schema, v.name)
		}
	}

	funcRows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, p.proname, pg_get_function_identity_arguments(p.oid), "+
			"pg_get_userbyid(p.proowner), p.proacl, p.prokind FROM pg_catalog.pg_proc p "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND p.prokind IN ('f', 'p') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = p.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, p.proname")
	if err != nil {
		return nil
	}
	type funcRef struct{ schema, name, args, owner, acl, kind string }
	var funcs []funcRef
	for funcRows.Next() {
		var f funcRef
		var aclVal sql.NullString
		if err := funcRows.Scan(&f.schema, &f.name, &f.args, &f.owner, &aclVal, &f.kind); err != nil {
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
		objType := "FUNCTION"
		if f.kind == "p" {
			objType = "PROCEDURE"
		}
		ident := quotePGIdent(schema) + "." + quotePGIdent(name) + "(" + args + ")"
		emitted := false
		if owner != "" {
			header()
			if !emitted {
				fmt.Fprintf(w, "\n-- ACL: %s %s.%s(%s)\n", objType, schema, name, args)
				emitted = true
			}
			fmt.Fprintf(w, "ALTER %s %s OWNER TO %s;\n", objType, ident, quotePGRole(owner))
		}
		if f.acl != "" && f.acl != "{}" {
			grants, err := d.funcGrants(schema, name, args)
			if err != nil {
				log.Warn("postgres", "grants unavailable", "database", dbName,
					"object", objType+" "+schema+"."+name, "error", err.Error())
			} else if len(grants) > 0 {
				grantCount += len(grants)
				log.Trace("postgres", "grants dumped", "database", dbName,
					"object", objType+" "+schema+"."+name, "grants", len(grants))
				header()
				if !emitted {
					fmt.Fprintf(w, "\n-- ACL: %s %s.%s(%s)\n", objType, schema, name, args)
				}
				fmt.Fprint(w, formatGrants(objType+" "+ident, grants))
			}
		}
	}
	log.Debug("postgres", "ACLs done", "database", dbName, "grants", grantCount)
	return nil
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

func (d *Dumper) listPublications() ([]string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT p.pubname FROM pg_catalog.pg_publication p ORDER BY 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (d *Dumper) dumpPublications(w io.Writer, dbName string) error {
	hasCol := func(rel, col string) bool {
		var has bool
		if err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_attribute "+
				"WHERE attrelid = ('pg_catalog.' || $1)::regclass AND attname = $2)", rel, col).Scan(&has); err != nil {
			return false
		}
		return has
	}
	viarootSel := "false"
	if hasCol("pg_publication", "pubviaroot") {
		viarootSel = "p.pubviaroot"
	}
	gencolsSel := "NULL"
	if hasCol("pg_publication", "pubgencols") {
		gencolsSel = "p.pubgencols"
	}
	hasAttrs := hasCol("pg_publication_rel", "prattrs")
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT p.pubname, pg_get_userbyid(p.pubowner), p.puballtables, "+
			"p.pubinsert, p.pubupdate, p.pubdelete, p.pubtruncate, "+
			viarootSel+", "+gencolsSel+" "+
			"FROM pg_catalog.pg_publication p ORDER BY p.pubname")
	if err != nil {
		return fmt.Errorf("query publications: %w", err)
	}
	defer rows.Close()
	type pubRef struct {
		name, owner                   string
		all                           bool
		ins, upd, del, trunc, viaroot bool
		gencols                       sql.NullString
	}
	var pubs []pubRef
	for rows.Next() {
		var p pubRef
		if err := rows.Scan(&p.name, &p.owner, &p.all,
			&p.ins, &p.upd, &p.del, &p.trunc, &p.viaroot, &p.gencols); err != nil {
			return fmt.Errorf("scan publication row: %w", err)
		}
		pubs = append(pubs, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	relAttrs := "NULL AS attrs"
	if hasAttrs {
		relAttrs = "pr.prattrs::text AS attrs"
	}
	for _, p := range pubs {
		var tables []string
		if !p.all {
			trows, err := d.conn.Query(d.ctxOrBg(),
				"SELECT n.nspname, c.relname, "+relAttrs+" "+
					"FROM pg_catalog.pg_publication_rel pr "+
					"JOIN pg_catalog.pg_class c ON c.oid = pr.prrelid "+
					"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
					"WHERE pr.prpubid = (SELECT oid FROM pg_catalog.pg_publication WHERE pubname = $1) "+
					"ORDER BY n.nspname, c.relname", p.name)
			if err != nil {
				return fmt.Errorf("query publication tables: %w", err)
			}
			type pubTable struct {
				schema, name, attrs string
			}
			var trefs []pubTable
			for trows.Next() {
				var tschema, tname string
				var attrs sql.NullString
				if err := trows.Scan(&tschema, &tname, &attrs); err != nil {
					trows.Close()
					return fmt.Errorf("scan publication table row: %w", err)
				}
				trefs = append(trefs, pubTable{tschema, tname, attrs.String})
			}
			trows.Close()
			if err := trows.Err(); err != nil {
				return err
			}
			for _, tr := range trefs {
				entry := quotePGIdent(tr.schema) + "." + quotePGIdent(tr.name)
				if cols := d.pubColumnNames(tr.schema, tr.name, tr.attrs); cols != "" {
					entry += " (" + cols + ")"
				}
				tables = append(tables, entry)
			}
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE PUBLICATION %s", quotePGIdent(p.name))
		if p.all {
			sb.WriteString(" FOR ALL TABLES")
		} else if len(tables) > 0 {
			sb.WriteString(" FOR TABLE " + strings.Join(tables, ", "))
		}
		ops := []string{}
		if p.ins {
			ops = append(ops, "insert")
		}
		if p.upd {
			ops = append(ops, "update")
		}
		if p.del {
			ops = append(ops, "delete")
		}
		if p.trunc {
			ops = append(ops, "truncate")
		}
		opts := []string{}
		if len(ops) != 4 {
			opts = append(opts, "publish = '"+strings.Join(ops, ", ")+"'")
		}
		if p.viaroot {
			opts = append(opts, "publish_via_partition_root = true")
		}
		if p.gencols.Valid {
			if gen, ok := map[string]string{"n": "none"}[p.gencols.String]; ok {
				opts = append(opts, "publish_generated_columns = "+gen)
			}
		}
		if len(opts) > 0 {
			sb.WriteString(" WITH (" + strings.Join(opts, ", ") + ")")
		}
		sb.WriteString(";")
		log.Trace("postgres", "publication dumped", "database", dbName, "publication", p.name)
		fmt.Fprintf(w, "\n-- Publication: %s\n%s\n", p.name, sb.String())
		if p.owner != "" {
			fmt.Fprintf(w, "ALTER PUBLICATION %s OWNER TO %s;\n", quotePGIdent(p.name), quotePGRole(p.owner))
		}
	}
	return nil
}

func (d *Dumper) pubColumnNames(schema, table, attrs string) string {
	attrs = strings.TrimSpace(attrs)
	if attrs == "" || attrs == "{}" {
		return ""
	}
	var cols []string
	for _, an := range strings.Fields(attrs) {
		var col string
		err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT a.attname FROM pg_catalog.pg_attribute a "+
				"JOIN pg_catalog.pg_class c ON c.oid = a.attrelid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum = $3",
			schema, table, an).Scan(&col)
		if err != nil || col == "" {
			return ""
		}
		cols = append(cols, quotePGIdent(col))
	}
	return strings.Join(cols, ", ")
}

func (d *Dumper) dumpDefaultPrivileges(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT CASE WHEN d.defaclrole = 0 THEN NULL ELSE pg_get_userbyid(d.defaclrole) END, "+
			"n.nspname, d.defaclobjtype, "+
			"x.grantee::int, x.privilege_type, x.is_grantable "+
			"FROM pg_catalog.pg_default_acl d "+
			"LEFT JOIN pg_catalog.pg_namespace n ON n.oid = d.defaclnamespace "+
			"CROSS JOIN LATERAL aclexplode(d.defaclacl) x "+
			"WHERE n.oid IS NULL OR (n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\\_%') "+
			"ORDER BY 1, 2, 3, 5")
	if err != nil {
		return fmt.Errorf("query default privileges: %w", err)
	}
	defer rows.Close()
	type key struct {
		role, schema, obj, priv string
		grantable               bool
	}
	groups := map[key][]string{}
	var order []key
	type rawPriv struct {
		role, schema, obj, priv string
		grantee                 int32
		grantable               bool
	}
	var raw []rawPriv
	for rows.Next() {
		var role, schema sql.NullString
		var obj string
		var grantee int32
		var priv string
		var grantable bool
		if err := rows.Scan(&role, &schema, &obj, &grantee, &priv, &grantable); err != nil {
			return fmt.Errorf("scan default privilege row: %w", err)
		}
		raw = append(raw, rawPriv{role.String, schema.String, obj, priv, grantee, grantable})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, r := range raw {
		var objWord string
		switch r.obj {
		case "r":
			objWord = "TABLES"
		case "S":
			objWord = "SEQUENCES"
		case "f":
			objWord = "FUNCTIONS"
		case "T":
			objWord = "TYPES"
		default:
			continue
		}
		to := "PUBLIC"
		if r.grantee != 0 {
			var rn string
			if err := d.conn.QueryRow(d.ctxOrBg(),
				"SELECT rolname FROM pg_catalog.pg_roles WHERE oid = $1", r.grantee).Scan(&rn); err != nil || rn == "" {
				continue
			}
			to = quotePGRole(rn)
		}
		k := key{r.role, r.schema, objWord, r.priv, r.grantable}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], to)
	}
	for _, k := range order {
		var sb strings.Builder
		sb.WriteString("ALTER DEFAULT PRIVILEGES")
		if k.role != "" {
			sb.WriteString(" FOR ROLE " + quotePGRole(k.role))
		}
		if k.schema != "" {
			sb.WriteString(" IN SCHEMA " + quotePGIdent(k.schema))
		}
		sb.WriteString(" GRANT " + k.priv + " ON " + k.obj + " TO " + strings.Join(groups[k], ", "))
		if k.grantable {
			sb.WriteString(" WITH GRANT OPTION")
		}
		sb.WriteString(";")
		log.Trace("postgres", "default privileges dumped", "database", dbName,
			"statement", sb.String())
		fmt.Fprintf(w, "\n-- Default privileges\n%s\n", sb.String())
	}
	return nil
}

func (d *Dumper) dumpBlobs(w io.Writer, dbName string, tables []string) error {
	if d.SchemaOnly {
		return nil
	}
	loCols := map[string][]string{}
	for _, t := range tables {
		parts := strings.SplitN(t, ".", 2)
		if len(parts) != 2 {
			continue
		}
		cols, err := d.loColumns(parts[0], parts[1])
		if err != nil {
			log.Warn("postgres", "lo columns unavailable", "database", dbName,
				"table", t, "error", err.Error())
			continue
		}
		if len(cols) > 0 {
			loCols[t] = cols
		}
	}
	if len(loCols) == 0 {
		return d.dumpOrphanBlobs(w, dbName, map[uint32]bool{})
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
				log.Warn("postgres", "lo oids unreadable", "database", dbName,
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
	return d.writeBlobs(w, dbName, ordered)
}

func (d *Dumper) dumpOrphanBlobs(w io.Writer, dbName string, seen map[uint32]bool) error {
	rows, err := d.conn.Query(d.ctxOrBg(), "SELECT oid FROM pg_catalog.pg_largeobject_metadata ORDER BY oid")
	if err != nil {
		return fmt.Errorf("read orphan large objects: %w", err)
	}
	defer rows.Close()
	var ordered []uint32
	for rows.Next() {
		var oid uint32
		if err := rows.Scan(&oid); err != nil {
			return fmt.Errorf("scan orphan large object: %w", err)
		}
		if !seen[oid] {
			seen[oid] = true
			ordered = append(ordered, oid)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ordered) == 0 {
		return nil
	}
	log.Debug("postgres", "orphan large objects found", "database", dbName, "objects", len(ordered))
	return d.writeBlobs(w, dbName, ordered)
}

func (d *Dumper) writeBlobs(w io.Writer, dbName string, ordered []uint32) error {
	start := time.Now()
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
	included = d.orderTablesByInheritance(dbName, included)

	if err := d.dumpDrops(w, dbName, included); err != nil {
		return err
	}

	if err := d.dumpSchemas(w, dbName, included); err != nil {
		return err
	}
	if err := d.dumpExtensions(w, dbName); err != nil {
		return err
	}
	if err := d.dumpCollations(w, dbName); err != nil {
		return err
	}
	domainChecks, err := d.dumpTypes(w, dbName)
	if err != nil {
		return err
	}
	if err := d.dumpLanguages(w, dbName); err != nil {
		return err
	}
	if err := d.dumpFunctions(w, dbName, false); err != nil {
		return err
	}
	if err := d.dumpAggregates(w, dbName); err != nil {
		return err
	}
	if len(domainChecks) > 0 {
		fmt.Fprintf(w, "\n-- Domain checks\n")
		for _, chk := range domainChecks {
			fmt.Fprint(w, chk)
		}
	}

	if err := d.dumpForeignServers(w, dbName); err != nil {
		return err
	}
	if err := d.dumpUserMappings(w, dbName); err != nil {
		return err
	}
	if err := d.dumpForeignTables(w, dbName); err != nil {
		return err
	}
	if err := d.dumpTextSearch(w, dbName); err != nil {
		return err
	}
	if err := d.dumpCasts(w, dbName); err != nil {
		return err
	}
	if err := d.dumpConversions(w, dbName); err != nil {
		return err
	}
	if err := d.dumpOpClasses(w, dbName); err != nil {
		return err
	}
	if err := d.dumpEventTriggers(w, dbName); err != nil {
		return err
	}
	if err := d.dumpSequences(w, dbName); err != nil {
		return err
	}

	postTables := append([]string{}, included...)
	var attachDefs, fkDefs []string
	for _, table := range included {
		common.TraceTable(d.ctxOrBg(), dbName, table)
		partitionNames, attachSQL, fkSQL, err := d.dumpTable(w, dbName, table)
		if err != nil {
			return err
		}
		postTables = append(postTables, partitionNames...)
		if attachSQL != "" {
			attachDefs = append(attachDefs, attachSQL)
		}
		if fkSQL != "" {
			fkDefs = append(fkDefs, fkSQL)
		}
	}
	if len(attachDefs) > 0 {
		fmt.Fprintf(w, "\n-- Attach partitions\n")
		for _, a := range attachDefs {
			fmt.Fprint(w, a)
		}
	}
	if len(fkDefs) > 0 {
		fmt.Fprintf(w, "\n-- Foreign keys and exclusion constraints\n")
		for _, fk := range fkDefs {
			fmt.Fprint(w, fk)
		}
	}

	if err := d.dumpViews(w, dbName); err != nil {
		return err
	}
	matviewRefreshes, err := d.dumpMatviews(w, dbName)
	if err != nil {
		return err
	}
	if err := d.dumpFunctions(w, dbName, true); err != nil {
		return err
	}

	if err := d.dumpTriggers(w, dbName, postTables); err != nil {
		return err
	}
	if err := d.dumpRowSecurity(w, dbName, postTables); err != nil {
		return err
	}
	if err := d.dumpRules(w, dbName, postTables); err != nil {
		return err
	}
	if err := d.dumpStatistics(w, dbName, postTables); err != nil {
		return err
	}
	if err := d.dumpComments(w, dbName); err != nil {
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
	if err := d.dumpDefaultPrivileges(w, dbName); err != nil {
		return err
	}
	if err := d.dumpPublications(w, dbName); err != nil {
		return err
	}
	if len(matviewRefreshes) > 0 {
		fmt.Fprintf(w, "\n-- Materialized view data\n")
		for _, r := range matviewRefreshes {
			fmt.Fprint(w, r)
		}
	}
	log.Debug("postgres", "database done",
		"database", dbName, "tables", len(tables),
		"elapsed", time.Since(dbStart).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) orderTablesByInheritance(dbName string, tables []string) []string {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname || '.' || c.relname, pn.nspname || '.' || p.relname "+
			"FROM pg_catalog.pg_inherits i "+
			"JOIN pg_catalog.pg_class c ON c.oid = i.inhrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"JOIN pg_catalog.pg_class p ON p.oid = i.inhparent "+
			"JOIN pg_catalog.pg_namespace pn ON pn.oid = p.relnamespace "+
			"WHERE c.relispartition = false")
	if err != nil {
		log.Warn("postgres", "inheritance order unavailable", "database", dbName,
			"error", err.Error())
		return tables
	}
	defer rows.Close()
	parents := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			break
		}
		parents[child] = append(parents[child], parent)
	}
	if rows.Err() != nil {
		return tables
	}
	depth := map[string]int{}
	var compute func(t string, seen map[string]bool) int
	compute = func(t string, seen map[string]bool) int {
		if dd, ok := depth[t]; ok {
			return dd
		}
		if seen[t] {
			return 0
		}
		seen[t] = true
		max := 0
		for _, p := range parents[t] {
			if dd := compute(p, seen) + 1; dd > max {
				max = dd
			}
		}
		delete(seen, t)
		depth[t] = max
		return max
	}
	for _, t := range tables {
		compute(t, map[string]bool{})
	}
	out := append([]string{}, tables...)
	sort.SliceStable(out, func(i, j int) bool { return depth[out[i]] < depth[out[j]] })
	return out
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
		log.Warn("postgres", "sequence drops unavailable", "database", dbName, "error", err.Error())
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
		log.Warn("postgres", "type drops unavailable", "database", dbName, "error", err.Error())
	}
	if pubs, err := d.listPublications(); err == nil {
		for _, p := range pubs {
			fmt.Fprintf(w, "DROP PUBLICATION IF EXISTS %s;\n", quotePGIdent(p))
		}
	} else {
		log.Warn("postgres", "publication drops unavailable", "database", dbName, "error", err.Error())
	}
	return nil
}

func (d *Dumper) dumpExtensions(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"WITH RECURSIVE extdeps AS ("+
			"SELECT d.objid AS ext, d.refobjid AS req "+
			"FROM pg_catalog.pg_depend d "+
			"WHERE d.classid = 'pg_extension'::regclass "+
			"AND d.refclassid = 'pg_extension'::regclass), "+
			"extorder AS ("+
			"SELECT e.oid, 0 AS depth FROM pg_catalog.pg_extension e "+
			"WHERE NOT EXISTS (SELECT 1 FROM extdeps WHERE extdeps.ext = e.oid) "+
			"UNION "+
			"SELECT dd.ext, o.depth + 1 FROM extdeps dd "+
			"JOIN extorder o ON o.oid = dd.req WHERE o.depth < 32) "+
			"SELECT e.extname, n.nspname FROM pg_catalog.pg_extension e "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = e.extnamespace "+
			"LEFT JOIN (SELECT oid, MAX(depth) AS depth FROM extorder GROUP BY oid) o ON o.oid = e.oid "+
			"WHERE e.extname <> 'plpgsql' "+
			"ORDER BY COALESCE(o.depth, 0), e.extname")
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

type eventTriggerDef struct {
	name, event, function, enabled string
	tags                           []string
}

func buildEventTrigger(e eventTriggerDef) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP EVENT TRIGGER IF EXISTS %s;\n", quotePGIdent(e.name))
	fmt.Fprintf(&sb, "CREATE EVENT TRIGGER %s ON %s", quotePGIdent(e.name), quotePGIdent(e.event))
	if len(e.tags) > 0 {
		quoted := make([]string, 0, len(e.tags))
		for _, tag := range e.tags {
			quoted = append(quoted, "'"+escapePGLiteral(tag)+"'")
		}
		fmt.Fprintf(&sb, " WHEN TAG IN (%s)", strings.Join(quoted, ", "))
	}
	fmt.Fprintf(&sb, " EXECUTE FUNCTION %s;", e.function)
	switch e.enabled {
	case "D":
		fmt.Fprintf(&sb, "\nALTER EVENT TRIGGER %s DISABLE;", quotePGIdent(e.name))
	case "R":
		fmt.Fprintf(&sb, "\nALTER EVENT TRIGGER %s ENABLE REPLICA;", quotePGIdent(e.name))
	case "A":
		fmt.Fprintf(&sb, "\nALTER EVENT TRIGGER %s ENABLE ALWAYS;", quotePGIdent(e.name))
	}
	return sb.String()
}

func pgTagList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil
	}
	raw = strings.TrimPrefix(strings.TrimSuffix(raw, "}"), "{")
	var out []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.Trim(strings.TrimSpace(t), `"`)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (d *Dumper) dumpEventTriggers(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT e.evtname, e.evtevent, e.evttags::text, fn.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid), e.evtenabled FROM pg_catalog.pg_event_trigger e "+
			"JOIN pg_catalog.pg_proc p ON p.oid = e.evtfoid "+
			"JOIN pg_catalog.pg_namespace fn ON fn.oid = p.pronamespace "+
			"ORDER BY e.evtname")
	if err != nil {
		return fmt.Errorf("event triggers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e eventTriggerDef
		var schema, fn, args, tagsRaw string
		if err := rows.Scan(&e.name, &e.event, &tagsRaw, &schema, &fn, &args, &e.enabled); err != nil {
			return fmt.Errorf("scan event trigger: %w", err)
		}
		e.function = quotePGIdent(schema) + "." + quotePGIdent(fn) + "(" + args + ")"
		e.tags = pgTagList(tagsRaw)
		fmt.Fprintf(w, "\n-- Event trigger: %s\n%s\n", e.name, buildEventTrigger(e))
	}
	return rows.Err()
}

type optionPair struct {
	name, value string
}

func buildOptionsClause(opts []optionPair) string {
	if len(opts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		parts = append(parts, quotePGIdent(o.name)+" '"+escapePGLiteral(o.value)+"'")
	}
	return " OPTIONS (" + strings.Join(parts, ", ") + ")"
}

func buildForeignServer(name, fdw, owner string, opts []optionPair) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP SERVER IF EXISTS %s CASCADE;\n", quotePGIdent(name))
	fmt.Fprintf(&sb, "CREATE SERVER %s FOREIGN DATA WRAPPER %s%s;\n", quotePGIdent(name), quotePGIdent(fdw), buildOptionsClause(opts))
	fmt.Fprintf(&sb, "ALTER SERVER %s OWNER TO %s;", quotePGIdent(name), quotePGRole(owner))
	return sb.String()
}

func buildUserMapping(user, server string, opts []optionPair) string {
	forUser := "PUBLIC"
	if user != "" {
		forUser = quotePGIdent(user)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP USER MAPPING IF EXISTS FOR %s SERVER %s;\n", forUser, quotePGIdent(server))
	fmt.Fprintf(&sb, "CREATE USER MAPPING FOR %s SERVER %s%s;", forUser, quotePGIdent(server), buildOptionsClause(opts))
	return sb.String()
}

type foreignColumnDef struct {
	name, typ string
	notNull   bool
	options   []optionPair
}

func buildForeignTable(schema, name, server string, cols []foreignColumnDef, opts []optionPair) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP FOREIGN TABLE IF EXISTS %s.%s CASCADE;\n", quotePGIdent(schema), quotePGIdent(name))
	fmt.Fprintf(&sb, "CREATE FOREIGN TABLE %s.%s (\n", quotePGIdent(schema), quotePGIdent(name))
	for i, c := range cols {
		if i > 0 {
			sb.WriteString(",\n")
		}
		fmt.Fprintf(&sb, "    %s %s", quotePGIdent(c.name), c.typ)
		if c.notNull {
			sb.WriteString(" NOT NULL")
		}
		if len(c.options) > 0 {
			sb.WriteString(buildOptionsClause(c.options))
		}
	}
	fmt.Fprintf(&sb, "\n) SERVER %s%s;", quotePGIdent(server), buildOptionsClause(opts))
	return sb.String()
}

func (d *Dumper) dumpForeignServers(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT s.srvname, w.fdwname, r.rolname, o.option_name, o.option_value FROM pg_catalog.pg_foreign_server s "+
			"JOIN pg_catalog.pg_foreign_data_wrapper w ON w.oid = s.srvfdw "+
			"JOIN pg_catalog.pg_roles r ON r.oid = s.srvowner "+
			"LEFT JOIN LATERAL pg_catalog.pg_options_to_table(s.srvoptions) o ON true "+
			"WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = s.oid AND dd.deptype = 'e') "+
			"ORDER BY s.srvname, o.option_name")
	if err != nil {
		return fmt.Errorf("foreign servers: %w", err)
	}
	defer rows.Close()
	type serverDef struct {
		fdw, owner string
		opts       []optionPair
	}
	var order []string
	servers := map[string]*serverDef{}
	for rows.Next() {
		var name, fdw, owner string
		var optName, optValue sql.NullString
		if err := rows.Scan(&name, &fdw, &owner, &optName, &optValue); err != nil {
			return fmt.Errorf("scan foreign server: %w", err)
		}
		sd, ok := servers[name]
		if !ok {
			sd = &serverDef{fdw: fdw, owner: owner}
			servers[name] = sd
			order = append(order, name)
		}
		if optName.Valid {
			val := ""
			if optValue.Valid {
				val = optValue.String
			}
			sd.opts = append(sd.opts, optionPair{name: optName.String, value: val})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range order {
		sd := servers[name]
		fmt.Fprintf(w, "\n-- Foreign server: %s\n%s\n", name, buildForeignServer(name, sd.fdw, sd.owner, sd.opts))
	}
	return nil
}

func (d *Dumper) dumpUserMappings(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT s.srvname, r.rolname, o.option_name, o.option_value FROM pg_catalog.pg_user_mapping u "+
			"JOIN pg_catalog.pg_foreign_server s ON s.oid = u.umserver "+
			"LEFT JOIN pg_catalog.pg_roles r ON r.oid = u.umuser "+
			"LEFT JOIN LATERAL pg_catalog.pg_options_to_table(u.umoptions) o ON true "+
			"WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = u.oid AND dd.deptype = 'e') "+
			"ORDER BY s.srvname, r.rolname")
	if err != nil {
		return fmt.Errorf("user mappings: %w", err)
	}
	defer rows.Close()
	type mappingDef struct {
		user, server string
		opts         []optionPair
	}
	var order []string
	mappings := map[string]*mappingDef{}
	for rows.Next() {
		var server string
		var user, optName, optValue sql.NullString
		if err := rows.Scan(&server, &user, &optName, &optValue); err != nil {
			return fmt.Errorf("scan user mapping: %w", err)
		}
		key := server + "." + user.String
		md, ok := mappings[key]
		if !ok {
			md = &mappingDef{user: user.String, server: server}
			mappings[key] = md
			order = append(order, key)
		}
		if optName.Valid {
			val := ""
			if optValue.Valid {
				val = optValue.String
			}
			md.opts = append(md.opts, optionPair{name: optName.String, value: val})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range order {
		md := mappings[key]
		label := md.user
		if label == "" {
			label = "PUBLIC"
		}
		fmt.Fprintf(w, "\n-- User mapping: %s on %s\n%s\n", label, md.server, buildUserMapping(md.user, md.server, md.opts))
	}
	return nil
}

func (d *Dumper) dumpForeignTables(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.relname, c.oid, s.srvname FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"JOIN pg_catalog.pg_foreign_table ft ON ft.ftrelid = c.oid "+
			"JOIN pg_catalog.pg_foreign_server s ON s.oid = ft.ftserver "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, c.relname")
	if err != nil {
		return fmt.Errorf("foreign tables: %w", err)
	}
	defer rows.Close()
	type foreignTableDef struct {
		schema, name, server string
		relid                int64
	}
	var tables []foreignTableDef
	for rows.Next() {
		var t foreignTableDef
		if err := rows.Scan(&t.schema, &t.name, &t.server, &t.relid); err != nil {
			return fmt.Errorf("scan foreign table: %w", err)
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, t := range tables {
		qualified := t.schema + "." + t.name
		if d.Tables != nil {
			ok, _ := d.Tables.Apply(qualified)
			if !ok {
				if _, bare, cut := strings.Cut(qualified, "."); cut {
					ok, _ = d.Tables.Apply(bare)
				}
			}
			if !ok {
				log.Trace("postgres", "foreign table excluded by filter", "database", dbName, "table", qualified)
				continue
			}
		}
		common.TraceTable(d.ctxOrBg(), dbName, qualified)
		cols, err := d.foreignTableColumns(t.relid)
		if err != nil {
			return err
		}
		opts, err := d.tableOptions("SELECT pg_catalog.pg_options_to_table(ftoptions) FROM pg_catalog.pg_foreign_table WHERE ftrelid = $1", t.relid)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\n-- Foreign table: %s\n%s\n", qualified, buildForeignTable(t.schema, t.name, t.server, cols, opts))
	}
	return nil
}

func (d *Dumper) foreignTableColumns(relid int64) ([]foreignColumnDef, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod), a.attnotnull, o.option_name, o.option_value FROM pg_catalog.pg_attribute a "+
			"LEFT JOIN LATERAL pg_catalog.pg_options_to_table(a.attoptions) o ON true "+
			"WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum, o.option_name",
		relid)
	if err != nil {
		return nil, fmt.Errorf("foreign table columns: %w", err)
	}
	defer rows.Close()
	var cols []foreignColumnDef
	for rows.Next() {
		var name, typ string
		var notNull bool
		var optName, optValue sql.NullString
		if err := rows.Scan(&name, &typ, &notNull, &optName, &optValue); err != nil {
			return nil, fmt.Errorf("scan foreign table column: %w", err)
		}
		if len(cols) == 0 || cols[len(cols)-1].name != name {
			cols = append(cols, foreignColumnDef{name: name, typ: typ, notNull: notNull})
		}
		if optName.Valid {
			val := ""
			if optValue.Valid {
				val = optValue.String
			}
			cols[len(cols)-1].options = append(cols[len(cols)-1].options, optionPair{name: optName.String, value: val})
		}
	}
	return cols, rows.Err()
}

func (d *Dumper) tableOptions(query string, arg int64) ([]optionPair, error) {
	rows, err := d.conn.Query(d.ctxOrBg(), query, arg)
	if err != nil {
		return nil, fmt.Errorf("table options: %w", err)
	}
	defer rows.Close()
	var opts []optionPair
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scan table option: %w", err)
		}
		opts = append(opts, optionPair{name: name, value: value})
	}
	return opts, rows.Err()
}

func (d *Dumper) dumpFunctions(w io.Writer, dbName string, tableDependent bool) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT proname, pg_get_functiondef(oid) FROM pg_proc "+
			"WHERE pronamespace NOT IN ('pg_catalog'::regnamespace, 'information_schema'::regnamespace) "+
			"AND prokind IN ('f', 'p', 'w') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_depend dd WHERE dd.objid = pg_proc.oid AND dd.deptype = 'e') "+
			"AND (EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd "+
			"JOIN pg_catalog.pg_class c ON c.oid = dd.refobjid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE dd.objid = pg_proc.oid "+
			"AND dd.refclassid = 'pg_class'::regclass "+
			"AND c.relkind IN ('r', 'p', 'v', 'm', 'f') "+
			"AND n.nspname NOT IN ('pg_catalog', 'information_schema')) "+
			"OR EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd "+
			"JOIN pg_catalog.pg_type t ON t.oid = dd.refobjid "+
			"JOIN pg_catalog.pg_class c ON c.oid = t.typrelid "+
			"WHERE dd.objid = pg_proc.oid "+
			"AND dd.refclassid = 'pg_type'::regclass "+
			"AND t.typtype = 'c' "+
			"AND c.relkind IN ('r', 'p', 'v', 'm', 'f') "+
			"AND c.relnamespace NOT IN ('pg_catalog'::regnamespace, 'information_schema'::regnamespace))) = $1",
		tableDependent)
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

func buildLanguage(name string, trusted bool, handler, inline, validator string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP LANGUAGE IF EXISTS %s;\n", quotePGIdent(name))
	if trusted {
		fmt.Fprintf(&sb, "CREATE TRUSTED")
	} else {
		fmt.Fprintf(&sb, "CREATE")
	}
	fmt.Fprintf(&sb, " PROCEDURAL LANGUAGE %s", quotePGIdent(name))
	if handler != "" {
		fmt.Fprintf(&sb, " HANDLER %s", handler)
	}
	if inline != "" {
		fmt.Fprintf(&sb, " INLINE %s", inline)
	}
	if validator != "" {
		fmt.Fprintf(&sb, " VALIDATOR %s", validator)
	}
	sb.WriteString(";")
	return sb.String()
}

func (d *Dumper) dumpLanguages(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT l.lanname, l.lanpltrusted, hn.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid), "+
			"insp.nspname, ip.proname, pg_catalog.pg_get_function_identity_arguments(ip.oid), "+
			"vn.nspname, vp.proname, pg_catalog.pg_get_function_identity_arguments(vp.oid) "+
			"FROM pg_catalog.pg_language l "+
			"LEFT JOIN pg_catalog.pg_proc p ON p.oid = l.lanplcallfoid LEFT JOIN pg_catalog.pg_namespace hn ON hn.oid = p.pronamespace "+
			"LEFT JOIN pg_catalog.pg_proc ip ON ip.oid = l.laninline LEFT JOIN pg_catalog.pg_namespace insp ON insp.oid = ip.pronamespace "+
			"LEFT JOIN pg_catalog.pg_proc vp ON vp.oid = l.lanvalidator LEFT JOIN pg_catalog.pg_namespace vn ON vn.oid = vp.pronamespace "+
			"WHERE l.lanispl AND l.lanname NOT IN ('internal', 'c', 'sql') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = l.oid AND dd.deptype = 'e') "+
			"ORDER BY l.lanname")
	if err != nil {
		return fmt.Errorf("languages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var trusted bool
		var hSchema, hName, hArgs, iSchema, iName, iArgs, vSchema, vName, vArgs sql.NullString
		if err := rows.Scan(&name, &trusted, &hSchema, &hName, &hArgs, &iSchema, &iName, &iArgs, &vSchema, &vName, &vArgs); err != nil {
			return fmt.Errorf("scan language: %w", err)
		}
		qualified := func(s, n, a sql.NullString) string {
			if !n.Valid {
				return ""
			}
			args := ""
			if a.Valid {
				args = a.String
			}
			if s.Valid && s.String != "" {
				return quotePGIdent(s.String) + "." + quotePGIdent(n.String) + "(" + args + ")"
			}
			return quotePGIdent(n.String) + "(" + args + ")"
		}
		fmt.Fprintf(w, "\n-- Language: %s\n%s\n", name, buildLanguage(name, trusted, qualified(hSchema, hName, hArgs), qualified(iSchema, iName, iArgs), qualified(vSchema, vName, vArgs)))
	}
	return rows.Err()
}

func buildTextSearchParser(schema, name, start, token, end, headline, lextype string) string {
	return fmt.Sprintf("DROP TEXT SEARCH PARSER IF EXISTS %s.%s;\nCREATE TEXT SEARCH PARSER %s.%s (\n    START = %s,\n    GETTOKEN = %s,\n    END = %s,\n    HEADLINE = %s,\n    LEXTYPES = %s);",
		quotePGIdent(schema), quotePGIdent(name), quotePGIdent(schema), quotePGIdent(name), start, token, end, headline, lextype)
}

func buildTextSearchTemplate(schema, name, lexize string) string {
	return fmt.Sprintf("DROP TEXT SEARCH TEMPLATE IF EXISTS %s.%s;\nCREATE TEXT SEARCH TEMPLATE %s.%s (LEXIZE = %s);",
		quotePGIdent(schema), quotePGIdent(name), quotePGIdent(schema), quotePGIdent(name), lexize)
}

func buildTextSearchDictionary(schema, name, template, initoption string) string {
	opts := ""
	if strings.TrimSpace(initoption) != "" {
		opts = ", " + strings.TrimSpace(initoption)
	}
	return fmt.Sprintf("DROP TEXT SEARCH DICTIONARY IF EXISTS %s.%s;\nCREATE TEXT SEARCH DICTIONARY %s.%s (TEMPLATE = %s%s);",
		quotePGIdent(schema), quotePGIdent(name), quotePGIdent(schema), quotePGIdent(name), template, opts)
}

func buildTextSearchConfiguration(schema, name, parser string, mappings []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP TEXT SEARCH CONFIGURATION IF EXISTS %s.%s;\n", quotePGIdent(schema), quotePGIdent(name))
	fmt.Fprintf(&sb, "CREATE TEXT SEARCH CONFIGURATION %s.%s (PARSER = %s);", quotePGIdent(schema), quotePGIdent(name), parser)
	for _, m := range mappings {
		fmt.Fprintf(&sb, "\n%s;", m)
	}
	return sb.String()
}

func (d *Dumper) dumpTextSearch(w io.Writer, dbName string) error {
	parsers, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, p.prsname, hs.nspname, hp.proname, pg_catalog.pg_get_function_identity_arguments(hp.oid), "+
			"ts.nspname, tp.proname, pg_catalog.pg_get_function_identity_arguments(tp.oid), "+
			"es.nspname, ep.proname, pg_catalog.pg_get_function_identity_arguments(ep.oid), "+
			"ds.nspname, dp.proname, pg_catalog.pg_get_function_identity_arguments(dp.oid), "+
			"ls.nspname, lp.proname, pg_catalog.pg_get_function_identity_arguments(lp.oid) "+
			"FROM pg_catalog.pg_ts_parser p JOIN pg_catalog.pg_namespace n ON n.oid = p.prsnamespace "+
			"JOIN pg_catalog.pg_proc hp ON hp.oid = p.prsstart JOIN pg_catalog.pg_namespace hs ON hs.oid = hp.pronamespace "+
			"JOIN pg_catalog.pg_proc tp ON tp.oid = p.prstoken JOIN pg_catalog.pg_namespace ts ON ts.oid = tp.pronamespace "+
			"JOIN pg_catalog.pg_proc ep ON ep.oid = p.prsend JOIN pg_catalog.pg_namespace es ON es.oid = ep.pronamespace "+
			"JOIN pg_catalog.pg_proc dp ON dp.oid = p.prsheadline JOIN pg_catalog.pg_namespace ds ON ds.oid = dp.pronamespace "+
			"JOIN pg_catalog.pg_proc lp ON lp.oid = p.prslextype JOIN pg_catalog.pg_namespace ls ON ls.oid = lp.pronamespace "+
			"WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = p.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, p.prsname")
	if err != nil {
		return fmt.Errorf("text search parsers: %w", err)
	}
	defer parsers.Close()
	fn := func(s, n, a sql.NullString) string {
		args := ""
		if a.Valid {
			args = a.String
		}
		return quotePGIdent(s.String) + "." + quotePGIdent(n.String) + "(" + args + ")"
	}
	for parsers.Next() {
		var schema, name string
		var ss, sn, sa, ts, tn, ta, es, en, ea, ds, dn, da, ls, ln, la sql.NullString
		if err := parsers.Scan(&schema, &name, &ss, &sn, &sa, &ts, &tn, &ta, &es, &en, &ea, &ds, &dn, &da, &ls, &ln, &la); err != nil {
			return fmt.Errorf("scan text search parser: %w", err)
		}
		fmt.Fprintf(w, "\n-- Text search parser: %s.%s\n%s\n", schema, name,
			buildTextSearchParser(schema, name, fn(ss, sn, sa), fn(ts, tn, ta), fn(es, en, ea), fn(ds, dn, da), fn(ls, ln, la)))
	}
	parsers.Close()
	if err := parsers.Err(); err != nil {
		return err
	}
	templates, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, t.tmplname, ln.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid) "+
			"FROM pg_catalog.pg_ts_template t JOIN pg_catalog.pg_namespace n ON n.oid = t.tmplnamespace "+
			"JOIN pg_catalog.pg_proc p ON p.oid = t.tmpllexize JOIN pg_catalog.pg_namespace ln ON ln.oid = p.pronamespace "+
			"WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = t.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, t.tmplname")
	if err != nil {
		return fmt.Errorf("text search templates: %w", err)
	}
	defer templates.Close()
	for templates.Next() {
		var schema, name, lschema, lexize, args string
		if err := templates.Scan(&schema, &name, &lschema, &lexize, &args); err != nil {
			return fmt.Errorf("scan text search template: %w", err)
		}
		fmt.Fprintf(w, "\n-- Text search template: %s.%s\n%s\n", schema, name,
			buildTextSearchTemplate(schema, name, quotePGIdent(lschema)+"."+quotePGIdent(lexize)+"("+args+")"))
	}
	if err := templates.Err(); err != nil {
		return err
	}
	dicts, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, dd.dictname, tn.nspname, t.tmplname, dd.dictinitoption FROM pg_catalog.pg_ts_dict dd "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = dd.dictnamespace "+
			"JOIN pg_catalog.pg_ts_template t ON t.oid = dd.dicttemplate "+
			"JOIN pg_catalog.pg_namespace tn ON tn.oid = t.tmplnamespace "+
			"WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dx WHERE dx.objid = dd.oid AND dx.deptype = 'e') "+
			"ORDER BY n.nspname, dd.dictname")
	if err != nil {
		return fmt.Errorf("text search dictionaries: %w", err)
	}
	defer dicts.Close()
	for dicts.Next() {
		var schema, name, tschema, tmpl string
		var initoption sql.NullString
		if err := dicts.Scan(&schema, &name, &tschema, &tmpl, &initoption); err != nil {
			return fmt.Errorf("scan text search dictionary: %w", err)
		}
		opt := ""
		if initoption.Valid {
			opt = initoption.String
		}
		fmt.Fprintf(w, "\n-- Text search dictionary: %s.%s\n%s\n", schema, name,
			buildTextSearchDictionary(schema, name, quotePGIdent(tschema)+"."+quotePGIdent(tmpl), opt))
	}
	if err := dicts.Err(); err != nil {
		return err
	}
	configs, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.cfgname, pn.nspname, p.prsname, c.oid FROM pg_catalog.pg_ts_config c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.cfgnamespace "+
			"JOIN pg_catalog.pg_ts_parser p ON p.oid = c.cfgparser "+
			"JOIN pg_catalog.pg_namespace pn ON pn.oid = p.prsnamespace "+
			"WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, c.cfgname")
	if err != nil {
		return fmt.Errorf("text search configurations: %w", err)
	}
	defer configs.Close()
	type configDef struct {
		schema, name, parser string
		oid                  int64
	}
	var configDefs []configDef
	for configs.Next() {
		var cd configDef
		var pschema, parser string
		if err := configs.Scan(&cd.schema, &cd.name, &pschema, &parser, &cd.oid); err != nil {
			return fmt.Errorf("scan text search configuration: %w", err)
		}
		cd.parser = quotePGIdent(pschema) + "." + quotePGIdent(parser)
		configDefs = append(configDefs, cd)
	}
	if err := configs.Err(); err != nil {
		return err
	}
	for _, cd := range configDefs {
		maps, err := d.conn.Query(d.ctxOrBg(),
			"SELECT t.alias, d.dict FROM pg_catalog.ts_token_type((SELECT cfgparser FROM pg_catalog.pg_ts_config WHERE oid = $1)) AS t "+
				"LEFT JOIN LATERAL (SELECT n.nspname || '.' || dd.dictname AS dict FROM pg_catalog.pg_ts_config_map m JOIN pg_catalog.pg_ts_dict dd ON dd.oid = m.mapdict JOIN pg_catalog.pg_namespace n ON n.oid = dd.dictnamespace WHERE m.mapcfg = $1 AND m.maptokentype = t.tokid ORDER BY m.mapseqno) AS d ON true",
			cd.oid)
		if err != nil {
			return fmt.Errorf("text search mappings %s: %w", cd.name, err)
		}
		var mappings []string
		for maps.Next() {
			var alias string
			var dict sql.NullString
			if err := maps.Scan(&alias, &dict); err != nil {
				maps.Close()
				return fmt.Errorf("scan text search mapping %s: %w", cd.name, err)
			}
			if !dict.Valid {
				continue
			}
			mappings = append(mappings, fmt.Sprintf("ALTER TEXT SEARCH CONFIGURATION %s.%s ADD MAPPING FOR %s WITH %s", quotePGIdent(cd.schema), quotePGIdent(cd.name), alias, dict.String))
		}
		maps.Close()
		if err := maps.Err(); err != nil {
			return err
		}
		fmt.Fprintf(w, "\n-- Text search configuration: %s.%s\n%s\n", cd.schema, cd.name, buildTextSearchConfiguration(cd.schema, cd.name, cd.parser, mappings))
	}
	return nil
}

func buildCast(source, target, function, context string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP CAST IF EXISTS (%s AS %s);\n", source, target)
	fmt.Fprintf(&sb, "CREATE CAST (%s AS %s)", source, target)
	if function != "" {
		fmt.Fprintf(&sb, " WITH FUNCTION %s", function)
	}
	switch context {
	case "a":
		sb.WriteString(" AS ASSIGNMENT")
	case "i":
		sb.WriteString(" AS IMPLICIT")
	}
	sb.WriteString(";")
	return sb.String()
}

func (d *Dumper) dumpCasts(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT pg_catalog.format_type(c.castsource, NULL), pg_catalog.format_type(c.casttarget, NULL), "+
			"CASE WHEN c.castfuncid = 0 THEN '' ELSE pg_catalog.quote_ident(fn.nspname) || '.' || pg_catalog.quote_ident(p.proname) || '(' || pg_catalog.pg_get_function_identity_arguments(p.oid) || ')' END, c.castcontext::text "+
			"FROM pg_catalog.pg_cast c "+
			"LEFT JOIN pg_catalog.pg_proc p ON p.oid = c.castfuncid "+
			"LEFT JOIN pg_catalog.pg_namespace fn ON fn.oid = p.pronamespace "+
			"WHERE (c.castfuncid <> 0 AND fn.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')) "+
			"OR (c.castfuncid = 0 AND EXISTS (SELECT 1 FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace WHERE t.oid IN (c.castsource, c.casttarget) AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast'))) "+
			"ORDER BY 1, 2")
	if err != nil {
		return fmt.Errorf("casts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var source, target, function, context string
		if err := rows.Scan(&source, &target, &function, &context); err != nil {
			return fmt.Errorf("scan cast: %w", err)
		}
		fmt.Fprintf(w, "\n-- Cast: %s to %s\n%s\n", source, target, buildCast(source, target, function, context))
	}
	return rows.Err()
}

func buildConversion(name, source, target, function, def string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP CONVERSION IF EXISTS %s;\n", quotePGIdent(name))
	if def == "t" {
		fmt.Fprintf(&sb, "CREATE DEFAULT")
	} else {
		fmt.Fprintf(&sb, "CREATE")
	}
	fmt.Fprintf(&sb, " CONVERSION %s FOR '%s' TO '%s' FROM %s;", quotePGIdent(name), source, target, function)
	return sb.String()
}

func (d *Dumper) dumpConversions(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_catalog.pg_encoding_to_char(c.conforencoding), pg_catalog.pg_encoding_to_char(c.contoencoding), fn.nspname, p.proname, pg_catalog.pg_get_function_identity_arguments(p.oid), c.condefault::text "+
			"FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_proc p ON p.oid = c.conproc JOIN pg_catalog.pg_namespace fn ON fn.oid = p.pronamespace "+
			"WHERE c.connamespace <> 'pg_catalog'::regnamespace AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'e') "+
			"ORDER BY c.conname")
	if err != nil {
		return fmt.Errorf("conversions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, source, target, fschema, fn, args, def string
		if err := rows.Scan(&name, &source, &target, &fschema, &fn, &args, &def); err != nil {
			return fmt.Errorf("scan conversion: %w", err)
		}
		fmt.Fprintf(w, "\n-- Conversion: %s\n%s\n", name,
			buildConversion(name, source, target, quotePGIdent(fschema)+"."+quotePGIdent(fn)+"("+args+")", def))
	}
	return rows.Err()
}

func buildOpFamily(name, method string) string {
	return fmt.Sprintf("DROP OPERATOR FAMILY IF EXISTS %s USING %s;\nCREATE OPERATOR FAMILY %s USING %s;",
		quotePGIdent(name), quotePGIdent(method), quotePGIdent(name), quotePGIdent(method))
}

func buildOpClassDef(name, family, method, datatype, isDefault, members string) string {
	if isDefault == "t" {
		return fmt.Sprintf("DROP OPERATOR CLASS IF EXISTS %s USING %s;\nCREATE OPERATOR CLASS %s DEFAULT FOR TYPE %s USING %s FAMILY %s AS %s;",
			quotePGIdent(name), quotePGIdent(method), quotePGIdent(name), datatype, quotePGIdent(method), quotePGIdent(family), members)
	}
	return fmt.Sprintf("DROP OPERATOR CLASS IF EXISTS %s USING %s;\nCREATE OPERATOR CLASS %s FOR TYPE %s USING %s FAMILY %s AS %s;",
		quotePGIdent(name), quotePGIdent(method), quotePGIdent(name), datatype, quotePGIdent(method), quotePGIdent(family), members)
}

func (d *Dumper) dumpOpClasses(w io.Writer, dbName string) error {
	fams, err := d.conn.Query(d.ctxOrBg(),
		"SELECT o.opfname, a.amname FROM pg_catalog.pg_opfamily o JOIN pg_catalog.pg_am a ON a.oid = o.opfmethod "+
			"WHERE o.opfnamespace <> 'pg_catalog'::regnamespace AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = o.oid AND dd.deptype = 'e') "+
			"ORDER BY o.opfname")
	if err != nil {
		return fmt.Errorf("operator families: %w", err)
	}
	defer fams.Close()
	for fams.Next() {
		var name, method string
		if err := fams.Scan(&name, &method); err != nil {
			return fmt.Errorf("scan operator family: %w", err)
		}
		fmt.Fprintf(w, "\n-- Operator family: %s\n%s\n", name, buildOpFamily(name, method))
	}
	if err := fams.Err(); err != nil {
		return err
	}
	classes, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.opcname, f.opfname, a.amname, pg_catalog.format_type(c.opcintype, NULL), c.opcdefault::text, c.oid FROM pg_catalog.pg_opclass c "+
			"JOIN pg_catalog.pg_opfamily f ON f.oid = c.opcfamily JOIN pg_catalog.pg_am a ON a.oid = f.opfmethod "+
			"WHERE c.opcnamespace <> 'pg_catalog'::regnamespace AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'e') "+
			"ORDER BY c.opcname")
	if err != nil {
		return fmt.Errorf("operator classes: %w", err)
	}
	defer classes.Close()
	for classes.Next() {
		var name, family, method, datatype, isDefault string
		var oid int64
		if err := classes.Scan(&name, &family, &method, &datatype, &isDefault, &oid); err != nil {
			return fmt.Errorf("scan operator class: %w", err)
		}
		members, err := d.opClassMembers(oid)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\n-- Operator class: %s\n%s\n", name, buildOpClassDef(name, family, method, datatype, isDefault, members))
	}
	return classes.Err()
}

func (d *Dumper) opClassMembers(oid int64) (string, error) {
	ops, err := d.conn.Query(d.ctxOrBg(),
		"SELECT m.amopstrategy, pg_catalog.quote_ident(fn.nspname) || '.' || pg_catalog.quote_ident(p.proname) || '(' || pg_catalog.pg_get_function_identity_arguments(p.oid) || ')' "+
			"FROM pg_catalog.pg_amop m JOIN pg_catalog.pg_operator o ON o.oid = m.amopopr JOIN pg_catalog.pg_proc p ON p.oid = m.amopfunction JOIN pg_catalog.pg_namespace fn ON fn.oid = p.pronamespace "+
			"WHERE m.amopfamily = (SELECT opcfamily FROM pg_catalog.pg_opclass WHERE oid = $1) AND m.amopmethod = (SELECT opfmethod FROM pg_catalog.pg_opfamily WHERE oid = (SELECT opcfamily FROM pg_catalog.pg_opclass WHERE oid = $1)) AND m.amoplefttype = (SELECT opcintype FROM pg_catalog.pg_opclass WHERE oid = $1) AND m.amoprighttype = (SELECT opcintype FROM pg_catalog.pg_opclass WHERE oid = $1) "+
			"ORDER BY m.amopstrategy",
		oid)
	if err != nil {
		return "", fmt.Errorf("operator class operators: %w", err)
	}
	defer ops.Close()
	var members []string
	for ops.Next() {
		var strategy int
		var operator string
		if err := ops.Scan(&strategy, &operator); err != nil {
			return "", fmt.Errorf("scan operator class operator: %w", err)
		}
		members = append(members, fmt.Sprintf("OPERATOR %d %s", strategy, operator))
	}
	if err := ops.Err(); err != nil {
		return "", err
	}
	procs, err := d.conn.Query(d.ctxOrBg(),
		"SELECT m.amprocnum, pg_catalog.quote_ident(fn.nspname) || '.' || pg_catalog.quote_ident(p.proname) || '(' || pg_catalog.pg_get_function_identity_arguments(p.oid) || ')' "+
			"FROM pg_catalog.pg_amproc m JOIN pg_catalog.pg_proc p ON p.oid = m.amproc JOIN pg_catalog.pg_namespace fn ON fn.oid = p.pronamespace "+
			"WHERE m.amprocfamily = (SELECT opcfamily FROM pg_catalog.pg_opclass WHERE oid = $1) AND m.amproclefttype = (SELECT opcintype FROM pg_catalog.pg_opclass WHERE oid = $1) AND m.amprocrighttype = (SELECT opcintype FROM pg_catalog.pg_opclass WHERE oid = $1) "+
			"ORDER BY m.amprocnum",
		oid)
	if err != nil {
		return "", fmt.Errorf("operator class functions: %w", err)
	}
	defer procs.Close()
	for procs.Next() {
		var num int
		var fn string
		if err := procs.Scan(&num, &fn); err != nil {
			return "", fmt.Errorf("scan operator class function: %w", err)
		}
		members = append(members, fmt.Sprintf("FUNCTION %d %s", num, fn))
	}
	if err := procs.Err(); err != nil {
		return "", err
	}
	return strings.Join(members, ", "), nil
}

func (d *Dumper) dumpAggregates(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, p.proname, "+
			"CASE WHEN a.aggnumdirectargs > 0 THEN "+
			"(SELECT string_agg(format_type(u.oid::oid, NULL), ', ' ORDER BY u.ord) "+
			"FROM unnest(string_to_array(NULLIF(p.proargtypes::text, ''), ' ')) WITH ORDINALITY AS u(oid, ord) "+
			"WHERE u.ord <= a.aggnumdirectargs) ELSE '' END AS direct_args, "+
			"(SELECT string_agg(format_type(u.oid::oid, NULL), ', ' ORDER BY u.ord) "+
			"FROM unnest(string_to_array(NULLIF(p.proargtypes::text, ''), ' ')) WITH ORDINALITY AS u(oid, ord) "+
			"WHERE u.ord > a.aggnumdirectargs) AS agg_args, "+
			"a.aggkind::text, a.aggtransfn::regproc::text, format_type(a.aggtranstype, NULL), "+
			"CASE WHEN a.aggfinalfn = 0 THEN '' ELSE a.aggfinalfn::regproc::text END, "+
			"CASE WHEN a.aggcombinefn = 0 THEN '' ELSE a.aggcombinefn::regproc::text END, "+
			"CASE WHEN a.aggserialfn = 0 THEN '' ELSE a.aggserialfn::regproc::text END, "+
			"CASE WHEN a.aggdeserialfn = 0 THEN '' ELSE a.aggdeserialfn::regproc::text END, "+
			"CASE WHEN a.aggmtransfn = 0 THEN '' ELSE a.aggmtransfn::regproc::text END, "+
			"CASE WHEN a.aggminvtransfn = 0 THEN '' ELSE a.aggminvtransfn::regproc::text END, "+
			"CASE WHEN a.aggmfinalfn = 0 THEN '' ELSE a.aggmfinalfn::regproc::text END, "+
			"a.aggfinalextra, a.aggmfinalextra, a.aggfinalmodify::text, a.aggmfinalmodify::text, "+
			"a.aggtransspace, a.aggmtransspace, "+
			"CASE WHEN a.aggsortop = 0 THEN '' ELSE a.aggsortop::regoperator::text END, "+
			"a.agginitval, a.aggminitval, p.proparallel::text, p.provariadic "+
			"FROM pg_catalog.pg_proc p "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace "+
			"JOIN pg_catalog.pg_aggregate a ON a.aggfnoid = p.oid "+
			"WHERE p.prokind = 'a' "+
			"AND n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = p.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, p.proname")
	if err != nil {
		return fmt.Errorf("query aggregates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, name string
		var directArgs, aggArgs sql.NullString
		var kind, sfunc, stype string
		var finalfn, combinefn, serialfn, deserialfn, msfunc, minvfunc, mfinalfn string
		var finalextra, mfinalextra bool
		var finalmodify, mfinalmodify string
		var sspace, mspace int
		var sortop string
		var initcond, minitcond, parallel sql.NullString
		var provariadic uint32
		if err := rows.Scan(&schema, &name, &directArgs, &aggArgs, &kind,
			&sfunc, &stype, &finalfn, &combinefn, &serialfn, &deserialfn,
			&msfunc, &minvfunc, &mfinalfn, &finalextra, &mfinalextra,
			&finalmodify, &mfinalmodify, &sspace, &mspace, &sortop,
			&initcond, &minitcond, &parallel, &provariadic); err != nil {
			return fmt.Errorf("scan aggregate row: %w", err)
		}
		direct, agg := directArgs.String, aggArgs.String
		if provariadic != 0 {
			if agg != "" {
				agg = wrapVariadicLast(agg)
			} else {
				direct = wrapVariadicLast(direct)
			}
			if kind == "o" && direct != "" && !strings.HasPrefix(direct, "VARIADIC") {
				direct = wrapVariadicLast(direct)
			}
		}
		var sig string
		switch kind {
		case "o", "h":
			dd := direct
			if dd == "" {
				dd = agg
			}
			sig = dd + " ORDER BY " + agg
		default:
			sig = agg
		}
		if strings.TrimSpace(sig) == "" {
			sig = "*"
		}
		opts := []string{"SFUNC = " + sfunc, "STYPE = " + stype}
		if sspace != 0 {
			opts = append(opts, "SSPACE = "+strconv.Itoa(sspace))
		}
		if finalfn != "" {
			opt := "FINALFUNC = " + finalfn
			if finalextra {
				opt += " FINALFUNC_EXTRA"
			}
			switch finalmodify {
			case "s":
				opt += " FINALFUNC_MODIFY = SHAREABLE"
			case "w":
				opt += " FINALFUNC_MODIFY = READ_WRITE"
			}
			opts = append(opts, opt)
		}
		if combinefn != "" {
			opts = append(opts, "COMBINEFUNC = "+combinefn)
		}
		if serialfn != "" {
			opts = append(opts, "SERIALFUNC = "+serialfn)
		}
		if deserialfn != "" {
			opts = append(opts, "DESERIALFUNC = "+deserialfn)
		}
		if msfunc != "" {
			opts = append(opts, "MSFUNC = "+msfunc)
		}
		if minvfunc != "" {
			opts = append(opts, "MINVFUNC = "+minvfunc)
		}
		if mfinalfn != "" {
			opt := "MFINALFUNC = " + mfinalfn
			if mfinalextra {
				opt += " MFINALFUNC_EXTRA"
			}
			switch mfinalmodify {
			case "s":
				opt += " MFINALFUNC_MODIFY = SHAREABLE"
			case "w":
				opt += " MFINALFUNC_MODIFY = READ_WRITE"
			}
			opts = append(opts, opt)
		}
		if mspace != 0 {
			opts = append(opts, "MSPACE = "+strconv.Itoa(mspace))
		}
		if initcond.Valid && initcond.String != "" {
			opts = append(opts, "INITCOND = '"+strings.ReplaceAll(initcond.String, "'", "''")+"'")
		}
		if minitcond.Valid && minitcond.String != "" {
			opts = append(opts, "MINITCOND = '"+strings.ReplaceAll(minitcond.String, "'", "''")+"'")
		}
		if sortop != "" {
			opts = append(opts, "SORTOP = "+sortop)
		}
		if parallel.Valid {
			switch parallel.String {
			case "s":
				opts = append(opts, "PARALLEL = SAFE")
			case "r":
				opts = append(opts, "PARALLEL = RESTRICTED")
			case "u":
				opts = append(opts, "PARALLEL = UNSAFE")
			}
		}
		if kind == "h" {
			opts = append(opts, "HYPOTHETICAL")
		}
		log.Trace("postgres", "aggregate dumped", "database", dbName, "aggregate", schema+"."+name)
		fmt.Fprintf(w, "\n-- Aggregate: %s.%s(%s)\n", schema, name, sig)
		fmt.Fprintf(w, "CREATE AGGREGATE %s.%s(%s) (\n    %s\n);\n",
			quotePGIdent(schema), quotePGIdent(name), sig, strings.Join(opts, ",\n    "))
	}
	return rows.Err()
}

func wrapVariadicLast(sig string) string {
	depth := 0
	for i := len(sig) - 1; i >= 0; i-- {
		switch sig[i] {
		case ')':
			depth++
		case '(':
			depth--
		case ' ':
			if depth == 0 && i > 0 && sig[i-1] == ',' {
				head, last := sig[:i-1], strings.TrimSpace(sig[i+1:])
				return head + `, VARIADIC "` + last + `"`
			}
		}
	}
	return `VARIADIC "` + strings.TrimSpace(sig) + `"`
}

func (d *Dumper) DumpGlobals(w io.Writer) error {
	if d.ctx == nil {
		d.ctx = context.Background()
	}
	ctx := d.ctx

	fmt.Fprint(w, common.DumpBanner("--", "PostgreSQL globals",
		fmt.Sprintf("Host: %s  Server: %s", d.host, d.server.Display())))
	fmt.Fprintf(w, "--\n\n")

	rows, err := d.conn.Query(ctx,
		"SELECT rolname, rolsuper, rolinherit, rolcreaterole, rolcreatedb, rolcanlogin, rolreplication, rolconnlimit, rolvaliduntil FROM pg_roles WHERE rolname != 'postgres' ORDER BY rolname")
	if err != nil {
		return fmt.Errorf("query roles: %w", err)
	}
	for rows.Next() {
		var name string
		var super, inherit, createrole, createdb, canlogin, replication bool
		var connlimit int
		var validuntil sql.NullString
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

	if err := d.dumpTablespaces(w); err != nil {
		return err
	}

	fmt.Fprintf(w, "\n-- Globals dump completed\n")
	return rows.Err()
}

func buildTablespace(name, owner, location string, opts []optionPair) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP TABLESPACE IF EXISTS %s;\n", quotePGIdent(name))
	fmt.Fprintf(&sb, "CREATE TABLESPACE %s", quotePGIdent(name))
	fmt.Fprintf(&sb, " OWNER %s", quotePGIdent(owner))
	fmt.Fprintf(&sb, " LOCATION '%s'", strings.ReplaceAll(location, "'", "''"))
	if len(opts) > 0 {
		parts := make([]string, 0, len(opts))
		for _, o := range opts {
			parts = append(parts, quotePGIdent(o.name)+" = '"+escapePGLiteral(o.value)+"'")
		}
		fmt.Fprintf(&sb, " WITH (%s)", strings.Join(parts, ", "))
	}
	sb.WriteString(";")
	return sb.String()
}

func (d *Dumper) dumpTablespaces(w io.Writer) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT s.spcname, r.rolname, pg_catalog.pg_tablespace_location(s.oid), o.option_name, o.option_value FROM pg_catalog.pg_tablespace s "+
			"JOIN pg_catalog.pg_roles r ON r.oid = s.spcowner "+
			"LEFT JOIN LATERAL pg_catalog.pg_options_to_table(s.spcoptions) o ON true "+
			"WHERE s.spcname NOT LIKE 'pg\\_%' ORDER BY s.spcname, o.option_name")
	if err != nil {
		return fmt.Errorf("tablespaces: %w", err)
	}
	defer rows.Close()
	type spaceDef struct {
		owner, location string
		opts            []optionPair
	}
	var order []string
	spaces := map[string]*spaceDef{}
	for rows.Next() {
		var name, owner, location string
		var optName, optValue sql.NullString
		if err := rows.Scan(&name, &owner, &location, &optName, &optValue); err != nil {
			return fmt.Errorf("scan tablespace: %w", err)
		}
		if location == "" {
			continue
		}
		sd, ok := spaces[name]
		if !ok {
			sd = &spaceDef{owner: owner, location: location}
			spaces[name] = sd
			order = append(order, name)
		}
		if optName.Valid {
			val := ""
			if optValue.Valid {
				val = optValue.String
			}
			sd.opts = append(sd.opts, optionPair{name: optName.String, value: val})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(order) == 0 {
		return nil
	}
	fmt.Fprintf(w, "\n-- Tablespaces (locations are cluster-specific and may need editing)\n")
	for _, name := range order {
		sd := spaces[name]
		fmt.Fprintf(w, "%s\n", buildTablespace(name, sd.owner, sd.location, sd.opts))
	}
	return nil
}

func (d *Dumper) dumpPartitions(w io.Writer, dbName, schema, table string, schemaOnly bool, depth int) ([]string, string, string, error) {
	if depth > 8 {
		return nil, "", "", fmt.Errorf("partition nesting too deep at %s.%s", schema, table)
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
		return nil, "", "", fmt.Errorf("list partitions: %w", err)
	}
	type partRef struct{ schema, name, bound string }
	var parts []partRef
	for rows.Next() {
		var p partRef
		if err := rows.Scan(&p.schema, &p.name, &p.bound); err != nil {
			rows.Close()
			return nil, "", "", fmt.Errorf("scan partition row: %w", err)
		}
		parts = append(parts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", "", err
	}

	var dumped []string
	var attachSQL, conSQL strings.Builder
	for _, p := range parts {
		pschema, pname, bound := p.schema, p.name, p.bound
		qualified := pschema + "." + pname
		dumped = append(dumped, qualified)
		common.TraceTable(d.ctxOrBg(), dbName, qualified)
		log.Trace("postgres", "partition dumped", "database", dbName,
			"table", qualified, "parent", schema+"."+table)
		createSQL, err := d.getCreateTable(pschema, pname)
		if err != nil {
			return dumped, "", "", err
		}
		fmt.Fprintf(w, "\n-- Partition: %s (of %s.%s)\n%s\n\n", qualified, schema, table, createSQL)
		if owner, err := d.ownerOf(pschema, pname); err == nil && owner != "" {
			fmt.Fprintf(w, "ALTER TABLE %s.%s OWNER TO %s;\n\n",
				quotePGIdent(pschema), quotePGIdent(pname), quotePGRole(owner))
		} else if err != nil {
			log.Warn("postgres", "owner unavailable", "database", dbName,
				"table", pschema+"."+pname, "error", err.Error())
		}
		if !schemaOnly {
			if err := d.copyData(w, dbName, pschema, pname); err != nil {
				return dumped, "", "", err
			}
		}
		if d.isPartitioned(pschema, pname) {
			nested, nestedAttach, nestedCon, err := d.dumpPartitions(w, dbName, pschema, pname, schemaOnly, depth+1)
			if err != nil {
				return dumped, "", "", err
			}
			dumped = append(dumped, nested...)
			attachSQL.WriteString(nestedAttach)
			conSQL.WriteString(nestedCon)
		}
		if fk, err := d.getForeignKeys(pschema, pname); err == nil {
			conSQL.WriteString(fk)
		}
		if excl, err := d.getExclusionConstraints(pschema, pname); err == nil {
			conSQL.WriteString(excl)
		}
		fmt.Fprintf(&attachSQL, "ALTER TABLE ONLY %s.%s ATTACH PARTITION %s.%s %s;\n",
			quotePGIdent(schema), quotePGIdent(table),
			quotePGIdent(pschema), quotePGIdent(pname), bound)
	}
	return dumped, attachSQL.String(), conSQL.String(), nil
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
				quotePGIdent(s.name), quotePGRole(s.owner))
		}
		if grants, err := d.schemaGrants(s.name); err != nil {
			log.Warn("postgres", "schema grants unavailable", "database", dbName,
				"schema", s.name, "error", err.Error())
		} else if len(grants) > 0 {
			fmt.Fprint(w, formatGrants("SCHEMA "+quotePGIdent(s.name), grants))
		}
	}
	return nil
}

type userTypeRef struct{ schema, name, kind string }

func (d *Dumper) dumpSequences(w io.Writer, dbName string) error {
	start := time.Now()
	seqs, err := d.listStandaloneSequences()
	if err != nil {
		return err
	}
	for _, s := range seqs {
		schema, name, owner := s[0], s[1], s[2]
		qn := quotePGIdent(schema) + "." + quotePGIdent(name)
		var seqType, opts string
		err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT format_type(ps.seqtypid, NULL), "+
				"'START ' || ps.seqstart || ' INCREMENT BY ' || ps.seqincrement || "+
				"' MINVALUE ' || ps.seqmin || ' MAXVALUE ' || ps.seqmax || ' CACHE ' || ps.seqcache || "+
				"CASE WHEN ps.seqcycle THEN ' CYCLE' ELSE '' END "+
				"FROM pg_catalog.pg_sequence ps "+
				"JOIN pg_catalog.pg_class c ON c.oid = ps.seqrelid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2", schema, name).Scan(&seqType, &opts)
		if err != nil {
			log.Warn("postgres", "sequence skipped", "database", dbName,
				"sequence", schema+"."+name, "error", err.Error())
			continue
		}
		log.Trace("postgres", "sequence dumped", "database", dbName,
			"sequence", schema+"."+name)
		fmt.Fprintf(w, "\n-- Sequence: %s.%s\n", schema, name)
		fmt.Fprintf(w, "CREATE SEQUENCE IF NOT EXISTS %s AS %s %s;\n", qn, seqType, opts)
		if owner != "" {
			fmt.Fprintf(w, "ALTER SEQUENCE %s OWNER TO %s;\n", qn, quotePGRole(owner))
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
			log.Warn("postgres", "sequence value unreadable", "database", dbName,
				"sequence", schema+"."+name, "error", err.Error())
			continue
		}
		link()
		called := "false"
		if isCalled {
			called = "true"
		}
		fmt.Fprintf(w, "SELECT pg_catalog.setval('%s', %d, %s);\n",
			escapePGLiteral(schema+"."+quotePGIdent(name)), lastVal, called)
		log.Trace("postgres", "sequence value dumped", "database", dbName,
			"sequence", schema+"."+name, "last_value", lastVal, "is_called", isCalled)
	}
	return nil
}

func (d *Dumper) dumpTable(w io.Writer, dbName, table string) ([]string, string, string, error) {
	parts := strings.SplitN(table, ".", 2)
	schema := parts[0]
	tableName := parts[1]

	createSQL, err := d.getCreateTable(schema, tableName)
	if err != nil {
		return nil, "", "", err
	}
	fmt.Fprintf(w, "\n-- Table: %s.%s\n%s\n\n", schema, tableName, createSQL)
	if owner, err := d.ownerOf(schema, tableName); err == nil && owner != "" {
		fmt.Fprintf(w, "ALTER TABLE %s.%s OWNER TO %s;\n\n",
			quotePGIdent(schema), quotePGIdent(tableName), quotePGRole(owner))
	} else if err != nil {
		log.Warn("postgres", "owner unavailable", "database", dbName,
			"table", schema+"."+tableName, "error", err.Error())
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

	var fkSQL string
	if fk, err := d.getForeignKeys(schema, tableName); err == nil {
		fkSQL = fk
	} else {
		log.Warn("postgres", "foreign keys unavailable", "database", dbName,
			"table", schema+"."+tableName, "error", err.Error())
	}
	if excl, err := d.getExclusionConstraints(schema, tableName); err == nil {
		fkSQL += excl
	} else {
		log.Warn("postgres", "exclusion constraints unavailable", "database", dbName,
			"table", schema+"."+tableName, "error", err.Error())
	}
	if d.isPartitioned(schema, tableName) {
		log.Trace("postgres", "partitioned parent, DDL only", "database", dbName, "table", table)
		partitionNames, partAttach, partCon, err := d.dumpPartitions(w, dbName, schema, tableName, schemaOnly, 0)
		return partitionNames, partAttach, fkSQL + partCon, err
	}
	if schemaOnly {
		return nil, "", fkSQL, nil
	}

	if err := d.copyData(w, dbName, schema, tableName); err != nil {
		return nil, "", "", err
	}

	return nil, "", fkSQL, nil
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

func (d *Dumper) dumpRowSecurity(w io.Writer, dbName string, tables []string) error {
	start := time.Now()
	count := 0
	for _, t := range tables {
		parts := strings.SplitN(t, ".", 2)
		if len(parts) != 2 {
			continue
		}
		schema, table := parts[0], parts[1]
		var enabled, force bool
		err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT c.relrowsecurity, c.relforcerowsecurity "+
				"FROM pg_catalog.pg_class c "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2", schema, table).Scan(&enabled, &force)
		if err != nil || !enabled {
			continue
		}
		qt := quotePGIdent(schema) + "." + quotePGIdent(table)
		fmt.Fprintf(w, "\n-- Row security: %s.%s\n", schema, table)
		fmt.Fprintf(w, "ALTER TABLE %s ENABLE ROW LEVEL SECURITY;\n", qt)
		if force {
			fmt.Fprintf(w, "ALTER TABLE %s FORCE ROW LEVEL SECURITY;\n", qt)
		}
		rows, err := d.conn.Query(d.ctxOrBg(),
			"SELECT p.polname, p.polpermissive, "+
				"CASE WHEN p.polroles = '{0}'::oid[] THEN 'PUBLIC' "+
				"ELSE (SELECT string_agg(quote_ident(r2.rolname), ', ' ORDER BY r2.rolname) FROM unnest(p.polroles) AS r JOIN pg_catalog.pg_roles r2 ON r2.oid = r) END, "+
				"p.polcmd, pg_get_expr(p.polqual, p.polrelid), pg_get_expr(p.polwithcheck, p.polrelid) "+
				"FROM pg_catalog.pg_policy p "+
				"JOIN pg_catalog.pg_class c ON c.oid = p.polrelid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2 ORDER BY p.polname", schema, table)
		if err != nil {
			log.Trace("postgres", "policies unavailable", "database", dbName,
				"table", t, "error", err.Error())
			continue
		}
		for rows.Next() {
			var name string
			var permissive bool
			var roles *string
			var cmd string
			var qual, withCheck *string
			if err := rows.Scan(&name, &permissive, &roles, &cmd, &qual, &withCheck); err != nil {
				break
			}
			forCmd := map[string]string{"r": "SELECT", "a": "INSERT", "w": "UPDATE", "d": "DELETE", "*": "ALL"}[cmd]
			if forCmd == "" {
				continue
			}
			to := "PUBLIC"
			if roles != nil && *roles != "" {
				to = *roles
			}
			as := "PERMISSIVE"
			if !permissive {
				as = "RESTRICTIVE"
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "CREATE POLICY %s ON %s AS %s FOR %s TO %s",
				quotePGIdent(name), qt, as, forCmd, to)
			if qual != nil && *qual != "" && cmd != "a" {
				fmt.Fprintf(&sb, " USING (%s)", *qual)
			}
			if withCheck != nil && *withCheck != "" && cmd != "r" && cmd != "d" {
				fmt.Fprintf(&sb, " WITH CHECK (%s)", *withCheck)
			}
			sb.WriteString(";\n")
			count++
			log.Trace("postgres", "policy dumped", "database", dbName,
				"table", t, "policy", name)
			fmt.Fprintf(w, "DROP POLICY IF EXISTS %s ON %s;\n", quotePGIdent(name), qt)
			fmt.Fprint(w, sb.String())
		}
		rows.Close()
	}
	log.Debug("postgres", "row security done", "database", dbName,
		"count", count, "elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dumpRules(w io.Writer, dbName string, tables []string) error {
	start := time.Now()
	count := 0
	for _, t := range tables {
		parts := strings.SplitN(t, ".", 2)
		if len(parts) != 2 {
			continue
		}
		schema, table := parts[0], parts[1]
		rows, err := d.conn.Query(d.ctxOrBg(),
			"SELECT r.rulename, pg_get_ruledef(r.oid) "+
				"FROM pg_catalog.pg_rewrite r "+
				"JOIN pg_catalog.pg_class c ON c.oid = r.ev_class "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
				"WHERE n.nspname = $1 AND c.relname = $2 AND r.rulename <> '_RETURN' "+
				"ORDER BY r.rulename", schema, table)
		if err != nil {
			log.Trace("postgres", "rules unavailable", "database", dbName,
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
			log.Trace("postgres", "rule dumped", "database", dbName,
				"table", t, "rule", name)
			fmt.Fprintf(w, "\n-- Rule: %s (on %s.%s)\n", name, schema, table)
			fmt.Fprintf(w, "DROP RULE IF EXISTS %s ON %s.%s;\n",
				quotePGIdent(name), quotePGIdent(schema), quotePGIdent(table))
			fmt.Fprintf(w, "%s;\n", strings.TrimSuffix(strings.TrimSpace(def), ";"))
		}
		rows.Close()
	}
	log.Debug("postgres", "rules done", "database", dbName,
		"count", count, "elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dumpStatistics(w io.Writer, dbName string, tables []string) error {
	start := time.Now()
	included := map[string]bool{}
	for _, t := range tables {
		included[t] = true
	}
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT DISTINCT n.nspname, s.stxname, pg_get_statisticsobjdef(s.oid), r.rolname, tn.nspname, tc.relname "+
			"FROM pg_catalog.pg_statistic_ext s "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = s.stxnamespace "+
			"JOIN pg_catalog.pg_authid r ON r.oid = s.stxowner "+
			"JOIN pg_catalog.pg_depend dd ON dd.objid = s.oid AND dd.classid = 'pg_statistic_ext'::regclass AND dd.refclassid = 'pg_class'::regclass "+
			"JOIN pg_catalog.pg_class tc ON tc.oid = dd.refobjid "+
			"JOIN pg_catalog.pg_namespace tn ON tn.oid = tc.relnamespace "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend e WHERE e.objid = s.oid AND e.deptype = 'e') "+
			"ORDER BY n.nspname, s.stxname")
	if err != nil {
		log.Trace("postgres", "statistics unavailable", "database", dbName,
			"error", err.Error())
		return nil
	}
	type statRef struct {
		schema, name, def, owner, tabschema, tabname string
	}
	var stats []statRef
	for rows.Next() {
		var s statRef
		if err := rows.Scan(&s.schema, &s.name, &s.def, &s.owner, &s.tabschema, &s.tabname); err != nil {
			break
		}
		if s.def == "" {
			continue
		}
		if !included[s.tabschema+"."+s.tabname] {
			continue
		}
		stats = append(stats, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Trace("postgres", "statistics unavailable", "database", dbName,
			"error", err.Error())
		return nil
	}
	count := 0
	for _, s := range stats {
		def := strings.TrimSuffix(strings.TrimSpace(s.def), ";")
		if idx := strings.LastIndex(def, " FROM "); idx >= 0 {
			target := def[idx+6:]
			if !strings.Contains(target, ".") {
				def = def[:idx+6] + quotePGIdent(s.schema) + "." +
					quotePGIdent(strings.Trim(target, `"`))
			}
		}
		count++
		log.Trace("postgres", "statistics dumped", "database", dbName,
			"statistics", s.schema+"."+s.name)
		fmt.Fprintf(w, "\n-- Statistics: %s.%s\n", s.schema, s.name)
		fmt.Fprintf(w, "DROP STATISTICS IF EXISTS %s.%s;\n",
			quotePGIdent(s.schema), quotePGIdent(s.name))
		fmt.Fprintf(w, "%s;\n", def)
		fmt.Fprintf(w, "ALTER STATISTICS %s.%s OWNER TO %s;\n",
			quotePGIdent(s.schema), quotePGIdent(s.name), quotePGRole(s.owner))
	}
	log.Debug("postgres", "statistics done", "database", dbName,
		"count", count, "elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dbDefaultCollationOID() uint32 {
	var oid uint32
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT c.oid FROM pg_catalog.pg_database d "+
			"JOIN pg_catalog.pg_collation c ON c.collname = d.datcollate "+
			"WHERE d.datname = current_database() LIMIT 1").Scan(&oid)
	if err != nil {
		return 0
	}
	return oid
}

func (d *Dumper) dumpCollations(w io.Writer, dbName string) error {
	hasCol := func(col string) bool {
		var has bool
		if err := d.conn.QueryRow(d.ctxOrBg(),
			"SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_attribute "+
				"WHERE attrelid = 'pg_catalog.pg_collation'::regclass AND attname = $1)", col).Scan(&has); err != nil {
			return false
		}
		return has
	}
	localeParts := []string{}
	if hasCol("colllocale") {
		localeParts = append(localeParts, "c.colllocale")
	}
	if hasCol("colliculocale") {
		localeParts = append(localeParts, "c.colliculocale")
	}
	localeParts = append(localeParts, "c.collcollate")
	localeSel := "COALESCE(" + strings.Join(localeParts, ", ") + ")"
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.collname, c.collprovider, c.collisdeterministic, "+
			"c.collencoding, c.collcollate, c.collctype, "+localeSel+" "+
			"FROM pg_catalog.pg_collation c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.collnamespace "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'e') "+
			"ORDER BY n.nspname, c.collname")
	if err != nil {
		return fmt.Errorf("query collations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var schema, name string
		var provider string
		var deterministic bool
		var encoding int32
		var collate, ctype, locale sql.NullString
		if err := rows.Scan(&schema, &name, &provider, &deterministic,
			&encoding, &collate, &ctype, &locale); err != nil {
			return fmt.Errorf("scan collation row: %w", err)
		}
		opts := []string{}
		switch provider {
		case "i":
			opts = append(opts, "provider = icu")
		case "c":
			opts = append(opts, "provider = libc")
		}
		if locale.Valid && locale.String != "" {
			opts = append(opts, "locale = '"+strings.ReplaceAll(locale.String, "'", "''")+"'")
		} else {
			if collate.Valid && collate.String != "" {
				opts = append(opts, "lc_collate = '"+strings.ReplaceAll(collate.String, "'", "''")+"'")
			}
			if ctype.Valid && ctype.String != "" {
				opts = append(opts, "lc_ctype = '"+strings.ReplaceAll(ctype.String, "'", "''")+"'")
			}
		}
		if encoding != -1 {
			var enc string
			if err := d.conn.QueryRow(d.ctxOrBg(),
				"SELECT pg_catalog.pg_encoding_to_char($1)", encoding).Scan(&enc); err == nil && enc != "" {
				opts = append(opts, "encoding = '"+enc+"'")
			}
		}
		if !deterministic {
			opts = append(opts, "deterministic = false")
		}
		log.Trace("postgres", "collation dumped", "database", dbName, "collation", schema+"."+name)
		fmt.Fprintf(w, "\n-- Collation: %s.%s\nCREATE COLLATION %s.%s (%s);\n",
			schema, name, quotePGIdent(schema), quotePGIdent(name), strings.Join(opts, ", "))
	}
	return rows.Err()
}

func (d *Dumper) dumpComments(w io.Writer, dbName string) error {
	start := time.Now()
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.relname, c.relkind, d.objsubid, a.attname, d.description "+
			"FROM pg_catalog.pg_description d "+
			"JOIN pg_catalog.pg_class c ON c.oid = d.objoid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid AND a.attnum = d.objsubid "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'i') "+
			"	ORDER BY n.nspname, c.relname, d.objsubid")
	if err != nil {
		log.Warn("postgres", "comments unavailable", "database", dbName,
			"error", err.Error())
		return nil
	}
	type commentRef struct {
		schema, rel, kind, desc string
		subid                   int32
		col                     *string
	}
	var comments []commentRef
	for rows.Next() {
		var c commentRef
		if err := rows.Scan(&c.schema, &c.rel, &c.kind, &c.subid, &c.col, &c.desc); err != nil {
			break
		}
		comments = append(comments, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Warn("postgres", "comments unavailable", "database", dbName,
			"error", err.Error())
		return nil
	}
	extMembers, extErr := d.extensionMembers()
	if extErr != nil {
		log.Warn("postgres", "extension members unavailable", "database", dbName,
			"error", extErr.Error())
	}
	kindKw := map[string]string{
		"r": "TABLE", "p": "TABLE", "v": "VIEW", "m": "MATERIALIZED VIEW",
		"S": "SEQUENCE", "i": "INDEX",
	}
	count := 0
	for _, c := range comments {
		schema, rel, kind, desc := c.schema, c.rel, c.kind, c.desc
		if extMembers[schema+"."+rel] {
			continue
		}
		kw, ok := kindKw[kind]
		if !ok {
			continue
		}
		count++
		if c.subid == 0 {
			fmt.Fprintf(w, "\n-- Comment: %s %s.%s\n", kw, schema, rel)
			fmt.Fprintf(w, "COMMENT ON %s %s.%s IS '%s';\n", kw,
				quotePGIdent(schema), quotePGIdent(rel), escapePGLiteral(desc))
		} else if c.col != nil {
			fmt.Fprintf(w, "\n-- Comment: column %s.%s.%s\n", schema, rel, *c.col)
			fmt.Fprintf(w, "COMMENT ON COLUMN %s.%s.%s IS '%s';\n",
				quotePGIdent(schema), quotePGIdent(rel), quotePGIdent(*c.col), escapePGLiteral(desc))
		}
	}
	log.Debug("postgres", "comments done", "database", dbName,
		"count", count, "elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dumpTypes(w io.Writer, dbName string) ([]string, error) {
	start := time.Now()
	refs, err := d.listUserTypes()
	if err != nil {
		return nil, err
	}

	var domainChecks []string
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
		if kind == "d" {
			if chk, err := d.getDomainChecks(schema, name); err == nil && chk != "" {
				domainChecks = append(domainChecks, chk)
			} else if err != nil {
				log.Trace("postgres", "domain checks unavailable", "database", dbName,
					"type", schema+"."+name, "error", err.Error())
			}
		}
		log.Trace("postgres", "type dumped", "database", dbName,
			"type", schema+"."+name, "kind", kind)
		fmt.Fprintf(w, "\n-- Type: %s.%s\n%s\n", schema, name, def)
		if owner != "" {
			kw := "TYPE"
			if kind == "d" {
				kw = "DOMAIN"
			}
			fmt.Fprintf(w, "ALTER %s %s.%s OWNER TO %s;\n", kw,
				quotePGIdent(schema), quotePGIdent(name), quotePGRole(owner))
		}
		if grants, err := d.typeGrants(schema, name); err != nil {
			log.Warn("postgres", "grants unavailable", "database", dbName,
				"object", "TYPE "+schema+"."+name, "error", err.Error())
		} else if len(grants) > 0 {
			fmt.Fprint(w, formatGrants(
				"TYPE "+quotePGIdent(schema)+"."+quotePGIdent(name), grants))
		}
	}
	log.Debug("postgres", "types done", "database", dbName, "count", count,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	return domainChecks, nil
}

func (d *Dumper) dumpViews(w io.Writer, dbName string) error {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT table_schema, table_name, COALESCE(view_definition, '') FROM information_schema.views "+
			"WHERE table_schema NOT IN ('pg_catalog', 'information_schema')")
	if err != nil {
		return fmt.Errorf("query views: %w", err)
	}
	type viewRef struct{ schema, name, def string }
	var views []viewRef
	for rows.Next() {
		var v viewRef
		if err := rows.Scan(&v.schema, &v.name, &v.def); err != nil {
			rows.Close()
			return fmt.Errorf("scan view row: %w", err)
		}
		views = append(views, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read views: %w", err)
	}

	extMembers, extErr := d.extensionMembers()
	if extErr != nil {
		log.Warn("postgres", "extension members unavailable", "database", dbName,
			"error", extErr.Error())
	}

	for _, v := range views {
		schema, name, def := v.schema, v.name, v.def
		if def == "" {
			continue
		}
		if extMembers[schema+"."+name] {
			log.Trace("postgres", "skipped extension member", "database", dbName,
				"view", schema+"."+name)
			continue
		}
		fmt.Fprintf(w, "\n-- View: %s.%s\n", schema, name)
		def = strings.TrimSuffix(strings.TrimSpace(def), ";")
		fmt.Fprintf(w, "CREATE OR REPLACE VIEW %s.%s AS\n%s;\n",
			quotePGIdent(schema), quotePGIdent(name), def)
		if owner, err := d.ownerOf(schema, name); err == nil && owner != "" {
			fmt.Fprintf(w, "ALTER VIEW %s.%s OWNER TO %s;\n",
				quotePGIdent(schema), quotePGIdent(name), quotePGRole(owner))
		} else if err != nil {
			log.Warn("postgres", "owner unavailable", "database", dbName,
				"view", schema+"."+name, "error", err.Error())
		}
		fmt.Fprintf(w, "\n")
	}
	return nil
}

func (d *Dumper) dumpMatviews(w io.Writer, dbName string) ([]string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT schemaname, matviewname, matviewowner, definition, ispopulated "+
			"FROM pg_catalog.pg_matviews "+
			"WHERE schemaname NOT IN ('pg_catalog', 'information_schema') "+
			"ORDER BY schemaname, matviewname")
	if err != nil {
		return nil, fmt.Errorf("query matviews: %w", err)
	}
	type matviewRef struct {
		schema, name, owner, def string
		populated                bool
	}
	var matviews []matviewRef
	for rows.Next() {
		var m matviewRef
		if err := rows.Scan(&m.schema, &m.name, &m.owner, &m.def, &m.populated); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan matview row: %w", err)
		}
		matviews = append(matviews, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read matviews: %w", err)
	}

	var refreshes []string
	for _, m := range matviews {
		if m.def == "" {
			continue
		}
		qt := quotePGIdent(m.schema) + "." + quotePGIdent(m.name)
		fmt.Fprintf(w, "\n-- Materialized view: %s.%s\n", m.schema, m.name)
		fmt.Fprintf(w, "DROP MATERIALIZED VIEW IF EXISTS %s CASCADE;\n", qt)
		def := strings.TrimSuffix(strings.TrimSpace(m.def), ";")
		fmt.Fprintf(w, "CREATE MATERIALIZED VIEW %s AS\n%s\nWITH NO DATA;\n", qt, def)
		if m.owner != "" {
			fmt.Fprintf(w, "ALTER MATERIALIZED VIEW %s OWNER TO %s;\n", qt, quotePGRole(m.owner))
		}
		if idxSQL, err := d.getIndexes(m.schema, m.name); err == nil && idxSQL != "" {
			fmt.Fprint(w, idxSQL)
		}
		log.Trace("postgres", "matview dumped", "database", dbName,
			"matview", m.schema+"."+m.name)
		if m.populated {
			refreshes = append(refreshes, fmt.Sprintf("REFRESH MATERIALIZED VIEW %s;\n", qt))
		}
	}
	log.Debug("postgres", "matviews done", "database", dbName, "count", len(matviews))
	return refreshes, nil
}

func encodeCopyArrayRaw(rv reflect.Value, jsonCtx bool) string {
	elems := make([]string, rv.Len())
	for i := range elems {
		ev := rv.Index(i).Interface()
		if ev == nil {
			elems[i] = "NULL"
			continue
		}
		er := reflect.ValueOf(ev)
		if er.Kind() == reflect.Ptr {
			if er.IsNil() {
				elems[i] = "NULL"
				continue
			}
			ev = er.Elem().Interface()
			er = reflect.ValueOf(ev)
		}
		if er.Kind() == reflect.Array && er.Len() == 16 && er.Type().Elem().Kind() == reflect.Uint8 {
			var b [16]byte
			reflect.Copy(reflect.ValueOf(&b).Elem(), er)
			s := encodeCopyUUID(b[:])
			if needsArrayQuoting(s, ev) {
				elems[i] = quoteArrayElement(s)
			} else {
				elems[i] = s
			}
			continue
		}
		if er.Kind() == reflect.Slice && er.Type().Elem().Kind() == reflect.Uint8 {
			s := `\x` + hex.EncodeToString(er.Bytes())
			if needsArrayQuoting(s, ev) {
				elems[i] = quoteArrayElement(s)
			} else {
				elems[i] = s
			}
			continue
		}
		if er.Kind() == reflect.Slice || er.Kind() == reflect.Array {
			elems[i] = quoteArrayElement(encodeCopyArrayRaw(er, jsonCtx))
			continue
		}
		var s string
		switch er.Kind() {
		case reflect.Map:
			if jsonCtx {
				if raw, err := json.Marshal(ev); err == nil {
					s = string(raw)
				} else {
					s = fmt.Sprintf("%v", ev)
				}
			} else {
				s = encodeCopyHstoreRaw(er)
			}
		default:
			s, _ = encodeCopyValue(ev, 0)
		}
		if needsArrayQuoting(s, ev) {
			elems[i] = quoteArrayElement(s)
		} else {
			elems[i] = s
		}
	}
	return "{" + strings.Join(elems, ",") + "}"
}

func encodeCopyHstoreRaw(rv reflect.Value) string {
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
	return strings.Join(pairs, ", ")
}

const (
	pgOIDOIDBytea      = 17
	pgOIDOIDJSON       = 114
	pgOIDOIDUUID       = 2950
	pgOIDOIDJSONB      = 3802
	pgOIDOIDJSONArray  = 199
	pgOIDOIDJSONBArray = 3807
)

func encodeCopyFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func encodeCopyVec2(p pgtype.Vec2) string {
	return "(" + encodeCopyFloat(p.X) + "," + encodeCopyFloat(p.Y) + ")"
}

func encodeCopyVecList(ps []pgtype.Vec2, open, close string) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = encodeCopyVec2(p)
	}
	return open + strings.Join(parts, ",") + close
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

func encodeCopyTime(us int64) string {
	neg := us < 0
	if neg {
		us = -us
	}
	h, us := us/3_600_000_000, us%3_600_000_000
	m, us := us/60_000_000, us%60_000_000
	s, us := us/1_000_000, us%1_000_000
	out := fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	if us != 0 {
		out += strings.TrimRight(fmt.Sprintf(".%06d", us), "0")
	}
	if neg {
		out = "-" + out
	}
	return out
}

func encodeCopyUUID(b []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func encodeCopyValue(val any, oid uint32) (string, bool) {
	if oid == pgOIDOIDJSON || oid == pgOIDOIDJSONB {
		if val == nil {
			return "\\N", false
		}
		if s, ok := val.(string); ok {
			raw, _ := json.Marshal(s)
			return string(raw), false
		}
		if b, ok := val.([]byte); ok {
			return string(b), false
		}
		if raw, err := json.Marshal(val); err == nil {
			return string(raw), false
		}
		return fmt.Sprintf("%v", val), true
	}
	if oid == pgOIDOIDJSONArray || oid == pgOIDOIDJSONBArray {
		if val == nil {
			return "\\N", false
		}
		if rv := reflect.ValueOf(val); rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			return encodeCopyArrayRaw(rv, true), false
		}
		if raw, err := json.Marshal(val); err == nil {
			return string(raw), true
		}
		return fmt.Sprintf("%v", val), true
	}
	if oid == pgOIDOIDUUID {
		if val == nil {
			return "\\N", false
		}
		if s, ok := val.(string); ok {
			return s, false
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
		return v, false
	case []byte:
		if oid == pgOIDOIDJSON || oid == pgOIDOIDJSONB {
			return string(v), false
		}
		return "\\x" + hex.EncodeToString(v), false
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
	case pgtype.Time:
		if !v.Valid {
			return "\\N", false
		}
		return encodeCopyTime(v.Microseconds), false
	case pgtype.Bits:
		if !v.Valid {
			return "\\N", false
		}
		var sb strings.Builder
		for i := int32(0); i < v.Len; i++ {
			var b byte
			if int(i/8) < len(v.Bytes) {
				b = v.Bytes[i/8]
			}
			if b&(1<<(7-(i%8))) != 0 {
				sb.WriteByte('1')
			} else {
				sb.WriteByte('0')
			}
		}
		return sb.String(), false
	case pgtype.Point:
		if !v.Valid {
			return "\\N", false
		}
		return encodeCopyVec2(v.P), false
	case pgtype.Lseg:
		if !v.Valid {
			return "\\N", false
		}
		return "[" + encodeCopyVec2(v.P[0]) + "," + encodeCopyVec2(v.P[1]) + "]", false
	case pgtype.Box:
		if !v.Valid {
			return "\\N", false
		}
		return encodeCopyVec2(v.P[0]) + "," + encodeCopyVec2(v.P[1]), false
	case pgtype.Polygon:
		if !v.Valid {
			return "\\N", false
		}
		return encodeCopyVecList(v.P, "(", ")"), false
	case pgtype.Path:
		if !v.Valid {
			return "\\N", false
		}
		if v.Closed {
			return encodeCopyVecList(v.P, "(", ")"), false
		}
		return encodeCopyVecList(v.P, "[", "]"), false
	case pgtype.Line:
		if !v.Valid {
			return "\\N", false
		}
		return "{" + encodeCopyFloat(v.A) + "," + encodeCopyFloat(v.B) + "," + encodeCopyFloat(v.C) + "}", false
	case pgtype.Circle:
		if !v.Valid {
			return "\\N", false
		}
		return "<" + encodeCopyVec2(v.P) + "," + encodeCopyFloat(v.R) + ">", false
	case fmt.Stringer:
		return v.String(), false
	}
	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		return encodeCopyArrayRaw(rv, false), false
	case reflect.Map:
		return encodeCopyHstoreRaw(rv), false
	case reflect.Ptr:
		if rv.IsNil() {
			return "\\N", false
		}
		return encodeCopyValue(rv.Elem().Interface(), oid)
	}
	return fmt.Sprintf("%v", val), true
}

func escapePgCopy(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func escapePGLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
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
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("read extension members: %w", err)
	}
	return out, nil
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
	if len(order) > 0 {
		fmt.Fprintf(&sb, "REVOKE ALL ON %s FROM PUBLIC;\n", target)
	}
	for _, k := range order {
		to := k.grantee
		if to != "PUBLIC" {
			to = quotePGRole(to)
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

func (d *Dumper) getCheckConstraints(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"WHERE n.nspname = $1 AND t.relname = $2 AND c.contype = 'c' "+
			"AND c.conislocal "+
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
		fmt.Fprintf(&sb, "ALTER TABLE ONLY %s.%s ADD CONSTRAINT %s %s;\n",
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
			"       a.attidentity, a.attgenerated, a.attislocal, "+
			"       cn.nspname, c.collname "+
			"FROM pg_catalog.pg_attribute a "+
			"JOIN pg_catalog.pg_class c0 ON c0.oid = a.attrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c0.relnamespace "+
			"JOIN pg_catalog.pg_type t ON t.oid = a.atttypid "+
			"LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum AND a.atthasdef "+
			"LEFT JOIN pg_catalog.pg_collation c ON c.oid = a.attcollation "+
			"  AND a.attcollation NOT IN (0, t.typcollation) AND a.attcollation <> $3::oid "+
			"LEFT JOIN pg_catalog.pg_namespace cn ON cn.oid = c.collnamespace "+
			"WHERE n.nspname = $1 AND c0.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped "+
			"ORDER BY a.attnum", schema, table, d.dbDefaultCollationOID())
	if err != nil {
		return "", fmt.Errorf("get columns: %w", err)
	}

	type colDef struct {
		col, typ, identity, generated string
		defStr                        string
		collSchema, collName          string
		notNull, isLocal              bool
	}
	var cols []colDef
	for rows.Next() {
		var col, typ, identity, generated string
		var defExpr, collSchema, collName sql.NullString
		var notNull, isLocal bool
		if err := rows.Scan(&col, &typ, &notNull, &defExpr, &identity, &generated, &isLocal, &collSchema, &collName); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan column row: %w", err)
		}
		defStr := ""
		if defExpr.Valid {
			defStr = defExpr.String
		}
		cs, cn := "", ""
		if collSchema.Valid && collName.Valid {
			cs, cn = collSchema.String, collName.String
		}
		cols = append(cols, colDef{col, typ, identity, generated, defStr, cs, cn, notNull, isLocal})
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return "", scanErr
	}

	notNullNames := map[string]string{}
	if nnRows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT a.attname, c.conname "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(c.conkey) "+
			"WHERE n.nspname = $1 AND t.relname = $2 AND c.contype = 'n'", schema, table); err == nil {
		for nnRows.Next() {
			var att, con string
			if err := nnRows.Scan(&att, &con); err != nil {
				break
			}
			notNullNames[att] = con
		}
		nnRows.Close()
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "DROP TABLE IF EXISTS %s.%s;\n", quotePGIdent(schema), quotePGIdent(table))
	fmt.Fprintf(&sb, "CREATE TABLE %s.%s (", quotePGIdent(schema), quotePGIdent(table))
	inhParents, _ := d.getInheritParents(schema, table)
	var first = true
	for _, c := range cols {
		col, typ, identity, generated := c.col, c.typ, c.identity, c.generated
		defStr, notNull := c.defStr, c.notNull
		if first {
			first = false
		} else {
			sb.WriteString(",")
		}
		sb.WriteString("\n    " + quotePGIdent(col) + " " + typ)
		if len(inhParents) > 0 && !c.isLocal {
			continue
		}
		if c.collName != "" {
			sb.WriteString(" COLLATE " + quotePGIdent(c.collSchema) + "." + quotePGIdent(c.collName))
		}
		if nn, ok := notNullNames[col]; ok {
			sb.WriteString(" CONSTRAINT " + quotePGIdent(nn))
		}
		if notNull {
			sb.WriteString(" NOT NULL")
		}
		switch {
		case generated == "s":
			if defStr != "" {
				sb.WriteString(" GENERATED ALWAYS AS (" + defStr + ") STORED")
			}
		case identity == "a":
			sb.WriteString(" GENERATED ALWAYS AS IDENTITY")
			if seqDef, err := d.identitySequenceDef(schema, table, col); err == nil && seqDef != "" {
				sb.WriteString(" (" + seqDef + ")")
			} else if defStr != "" {
				sb.WriteString(" (" + defStr + ")")
			}
		case identity == "d":
			sb.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
			if seqDef, err := d.identitySequenceDef(schema, table, col); err == nil && seqDef != "" {
				sb.WriteString(" (" + seqDef + ")")
			} else if defStr != "" {
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
	if len(inhParents) > 0 {
		sb.WriteString(" INHERITS (" + strings.Join(inhParents, ", ") + ")")
	}
	sb.WriteString(";")

	if pk, err := d.getPrimaryKey(schema, table); err == nil && pk != "" {
		sb.WriteString("\n" + pk)
	}
	if uqSQL, err := d.getUniqueConstraints(schema, table); err == nil && uqSQL != "" {
		sb.WriteString("\n" + uqSQL)
	}
	if idxSQL, err := d.getIndexes(schema, table); err == nil {
		sb.WriteString("\n" + idxSQL)
	}
	if checkSQL, err := d.getCheckConstraints(schema, table); err == nil {
		sb.WriteString("\n" + checkSQL)
	}

	return sb.String(), nil
}

func (d *Dumper) identitySequenceDef(schema, table, column string) (string, error) {
	var seqSchema, seqName string
	var seqStart, seqIncrement, seqMin, seqMax, seqCache int64
	var seqCycle bool
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT sn.nspname, s.relname, ps.seqstart, ps.seqincrement, "+
			"ps.seqmin, ps.seqmax, ps.seqcache, ps.seqcycle "+
			"FROM pg_catalog.pg_depend dd "+
			"JOIN pg_catalog.pg_class s ON s.oid = dd.objid "+
			"JOIN pg_catalog.pg_namespace sn ON sn.oid = s.relnamespace "+
			"JOIN pg_catalog.pg_sequence ps ON ps.seqrelid = s.oid "+
			"JOIN pg_catalog.pg_class t ON t.oid = dd.refobjid "+
			"JOIN pg_catalog.pg_namespace tn ON tn.oid = t.relnamespace "+
			"JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = dd.refobjsubid "+
			"WHERE tn.nspname = $1 AND t.relname = $2 AND a.attname = $3 "+
			"AND dd.deptype = 'i' LIMIT 1", schema, table, column).Scan(
		&seqSchema, &seqName, &seqStart, &seqIncrement, &seqMin, &seqMax, &seqCache, &seqCycle)
	if err != nil {
		return "", err
	}
	def := "SEQUENCE NAME " + quotePGIdent(seqSchema) + "." + quotePGIdent(seqName) +
		fmt.Sprintf(" START WITH %d INCREMENT BY %d MINVALUE %d MAXVALUE %d CACHE %d",
			seqStart, seqIncrement, seqMin, seqMax, seqCache)
	if seqCycle {
		def += " CYCLE"
	}
	return def, nil
}

func (d *Dumper) getForeignKeys(schema, table string) (string, error) {
	only := "ONLY "
	if d.isPartitioned(schema, table) {
		only = ""
	}
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"WHERE n.nspname = $1 AND t.relname = $2 AND c.contype = 'f' "+
			"AND c.conislocal "+
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
		fmt.Fprintf(&sb, "ALTER TABLE %s%s.%s ADD CONSTRAINT %s %s;\n", only,
			quotePGIdent(schema), quotePGIdent(table), quotePGIdent(conname), def)
	}
	return sb.String(), rows.Err()
}

func (d *Dumper) getExclusionConstraints(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"WHERE n.nspname = $1 AND t.relname = $2 AND c.contype = 'x' "+
			"AND c.conislocal "+
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
		fmt.Fprintf(&sb, "ALTER TABLE ONLY %s.%s ADD CONSTRAINT %s %s;\n",
			quotePGIdent(schema), quotePGIdent(table), quotePGIdent(conname), def)
	}
	return sb.String(), rows.Err()
}

func (d *Dumper) getInheritParents(schema, table string) ([]string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT pn.nspname, p.relname "+
			"FROM pg_catalog.pg_inherits i "+
			"JOIN pg_catalog.pg_class c ON c.oid = i.inhrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"JOIN pg_catalog.pg_class p ON p.oid = i.inhparent "+
			"JOIN pg_catalog.pg_namespace pn ON pn.oid = p.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2 "+
			"AND c.relispartition = false AND p.relkind IN ('r', 'p') "+
			"ORDER BY pn.nspname, p.relname", schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pschema, pname string
		if err := rows.Scan(&pschema, &pname); err != nil {
			return nil, err
		}
		out = append(out, quotePGIdent(pschema)+"."+quotePGIdent(pname))
	}
	return out, rows.Err()
}

func (d *Dumper) getIndexes(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT i.indexrelid::regclass, pg_get_indexdef(i.indexrelid), i.indisprimary "+
			"FROM pg_catalog.pg_index i "+
			"JOIN pg_catalog.pg_class c ON c.oid = i.indrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary = false "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint xc "+
			"  WHERE xc.conindid = i.indexrelid)",
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
			"  AND c.contype = 'p' AND c.conislocal LIMIT 1", schema, table).Scan(&conname, &def)
	if err != nil {
		return "", err
	}
	if def == "" {
		return "", nil
	}
	return fmt.Sprintf("ALTER TABLE ONLY %s.%s ADD CONSTRAINT %s %s;",
		quotePGIdent(schema), quotePGIdent(table), quotePGIdent(conname), def), nil
}

func (d *Dumper) getUniqueConstraints(schema, table string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) "+
			"FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_class t ON t.oid = c.conrelid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace "+
			"WHERE n.nspname = $1 AND t.relname = $2 AND c.contype = 'u' "+
			"AND c.conislocal "+
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
		fmt.Fprintf(&sb, "ALTER TABLE ONLY %s.%s ADD CONSTRAINT %s %s;\n",
			quotePGIdent(schema), quotePGIdent(table), quotePGIdent(conname), def)
	}
	return sb.String(), rows.Err()
}

func (d *Dumper) isPartitioned(schema, table string) bool {
	var kind string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT c.relkind FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2", schema, table).Scan(&kind)
	return err == nil && kind == "p"
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

func (d *Dumper) listTables(dbName string) ([]string, error) {
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
	type tableRef struct{ schema, name string }
	var refs []tableRef
	for rows.Next() {
		var r tableRef
		if err := rows.Scan(&r.schema, &r.name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan table row: %w", err)
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read tables: %w", err)
	}

	extMembers, extErr := d.extensionMembers()
	if extErr != nil {
		log.Warn("postgres", "extension members unavailable", "database", dbName,
			"error", extErr.Error())
	}

	var tables []string
	for _, r := range refs {
		schema, table := r.schema, r.name
		if extMembers[schema+"."+table] {
			log.Trace("postgres", "skipped extension member", "database", dbName,
				"table", schema+"."+table)
			continue
		}
		tables = append(tables, schema+"."+table)
	}
	return tables, nil
}

type pgSchemaInfo struct {
	name  string
	owner string
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

func (d *Dumper) listStandaloneSequences() ([][3]string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, c.relname, pg_get_userbyid(c.relowner) "+
			"FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE c.relkind = 'S' "+
			"AND n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND n.nspname NOT LIKE 'pg\\_%' "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'e') "+
			"AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dd WHERE dd.objid = c.oid AND dd.deptype = 'i') "+
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

func (d *Dumper) listUserTypes() ([]userTypeRef, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT n.nspname, t.typname, t.typtype "+
			"FROM pg_catalog.pg_type t "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
			"WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') "+
			"AND n.nspname NOT LIKE 'pg\\_%' "+
			"AND t.typtype IN ('e', 'c', 'd', 'r') "+
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

func needsArrayQuoting(s string, ev any) bool {
	if _, ok := ev.(string); ok {
		return true
	}
	if s == "" || s == "NULL" {
		return true
	}
	return strings.ContainsAny(s, "{},\"\\ \t\n\r")
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
		guardSession(ctx, d.conn)
		return nil
	}
	ping := func() error {
		var ver string
		if err := d.conn.QueryRow(ctx, "SELECT VERSION()").Scan(&ver); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		d.serverVer = ver
		d.server = ParseServerVersion(ver)
		log.Debug("postgres", "connected",
			"host", d.host, "port", d.port, "server", ver)
		return nil
	}
	return common.WithConnectivity(ctx, "postgres", d.connCfg, probe, connect, ping)
}

func (d *Dumper) ownerOf(schema, name string) (string, error) {
	var owner string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT pg_get_userbyid(c.relowner) FROM pg_catalog.pg_class c "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace "+
			"WHERE n.nspname = $1 AND c.relname = $2", schema, name).Scan(&owner)
	return owner, err
}

func pgCopyTextCast(typid uint32, cat, elemcat string) bool {
	switch typid {
	case pgOIDOIDBytea, pgOIDOIDJSON, pgOIDOIDJSONB, pgOIDOIDJSONArray, pgOIDOIDJSONBArray, pgOIDOIDUUID:
		return false
	}
	switch cat {
	case "D", "T":
		return true
	case "B", "N", "S", "V", "E", "R":
		return false
	case "A":
		switch elemcat {
		case "D", "T":
			return true
		case "B", "N", "S", "V", "E", "R":
			return false
		}
		return true
	}
	return true
}

type pgGrant struct {
	grantee   string
	priv      string
	grantable bool
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

func plural(n int32) string {
	if n == 1 || n == -1 {
		return ""
	}
	return "s"
}

func quoteArrayElement(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func quotePGIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quotePGRole(name string) string {
	if len(name) >= 2 && strings.HasPrefix(name, `"`) && strings.HasSuffix(name, `"`) {
		name = strings.ReplaceAll(name[1:len(name)-1], `""`, `"`)
	}
	return quotePGIdent(name)
}

func quoteQualifiedPGIdent(qualified string) string {
	parts := strings.Split(qualified, ".")
	for i, p := range parts {
		parts[i] = quotePGIdent(p)
	}
	return strings.Join(parts, ".")
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

func (d *Dumper) SetConnectivity(cfg *config.ConnectivityConfig) {
	if cfg != nil {
		d.connCfg = cfg
	}
}

func (d *Dumper) SetTableFilter(f *config.TableFilter, schemaOnly bool) {
	d.Tables = f
	d.SchemaOnly = schemaOnly
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
		var mrSchema, mrName string
		if err := d.conn.QueryRow(ctx,
			"SELECT n2.nspname, t2.typname FROM pg_catalog.pg_range r "+
				"JOIN pg_catalog.pg_type rt ON rt.oid = r.rngtypid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = rt.typnamespace "+
				"JOIN pg_catalog.pg_type t2 ON t2.oid = r.rngmultitypid "+
				"JOIN pg_catalog.pg_namespace n2 ON n2.oid = t2.typnamespace "+
				"WHERE n.nspname = $1 AND rt.typname = $2", schema, name).
			Scan(&mrSchema, &mrName); err == nil {
			opts += ", multirange_type_name = " + quotePGIdent(mrSchema) + "." + quotePGIdent(mrName)
		}
		return fmt.Sprintf("DROP TYPE IF EXISTS %s;\nCREATE TYPE %s AS RANGE (%s);",
			qn, qn, opts), nil
	}
	return "", fmt.Errorf("unknown type kind %q", kind)
}

func (d *Dumper) getDomainChecks(schema, name string) (string, error) {
	rows, err := d.conn.Query(d.ctxOrBg(),
		"SELECT c.conname, pg_get_constraintdef(c.oid) FROM pg_catalog.pg_constraint c "+
			"JOIN pg_catalog.pg_type t ON t.oid = c.contypid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
			"WHERE n.nspname = $1 AND t.typname = $2 AND c.contype = 'c' ORDER BY c.conname",
		schema, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	qn := quotePGIdent(schema) + "." + quotePGIdent(name)
	var sb strings.Builder
	for rows.Next() {
		var conname, cdef string
		if err := rows.Scan(&conname, &cdef); err != nil {
			return "", err
		}
		if cdef == "" {
			continue
		}
		fmt.Fprintf(&sb, "ALTER DOMAIN %s ADD CONSTRAINT %s %s;\n",
			qn, quotePGIdent(conname), cdef)
	}
	return sb.String(), rows.Err()
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

func (d *Dumper) typeOwner(schema, name string) (string, error) {
	var owner string
	err := d.conn.QueryRow(d.ctxOrBg(),
		"SELECT pg_get_userbyid(t.typowner) FROM pg_catalog.pg_type t "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace "+
			"WHERE n.nspname = $1 AND t.typname = $2", schema, name).Scan(&owner)
	return owner, err
}

func (d *Dumper) writeFooter(w io.Writer) {
	fmt.Fprintf(w, "-- Dump completed\n")
}

func (d *Dumper) writeHeader(w io.Writer, dbNames []string) {
	fmt.Fprint(w, common.DumpBanner("--", "PostgreSQL",
		fmt.Sprintf("Host: %s  Server: %s", d.host, d.server.Display())))
	fmt.Fprintf(w, `--
`)
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

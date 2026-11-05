// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package postgres

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/engine/postgres"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func CheckSupport(ctx context.Context, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "postgres"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := pgx.Connect(ctx, postgres.ConnStr(user, pass, host, port, firstDB, tlsCfg))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var walLevel string
	if err := conn.QueryRow(ctx, "SHOW wal_level").Scan(&walLevel); err != nil {
		return fmt.Errorf("wal_level: %w", err)
	}
	if walLevel != "logical" {
		return fmt.Errorf("wal_level=%q - set wal_level=logical (plus max_replication_slots/max_wal_senders) to enable logical decoding", walLevel)
	}
	return nil
}

func EnsureLogicalSlots(ctx context.Context, host string, port int, user, pass, dbName string) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "postgres"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		user, pass, host, port, firstDB)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	for _, kind := range []string{"incr", "diff"} {
		slot := SlotName(dbName, kind)
		conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot)
		if _, err := conn.Exec(ctx,
			"SELECT pg_create_logical_replication_slot($1, 'test_decoding')", slot); err != nil {
			return fmt.Errorf("create slot %s: %w", slot, err)
		}
	}
	return nil
}

func IncrementalDump(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "postgres"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := pgx.Connect(ctx, postgres.ConnStr(user, pass, host, port, firstDB, tlsCfg))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	slot := SlotName(dbName, "incr")
	if strategy == "differential" {
		slot = SlotName(dbName, "diff")
	}
	exists, err := slotExists(ctx, conn, slot)
	if err != nil {
		return fmt.Errorf("slot check: %w", err)
	}
	if !exists {
		fmt.Fprintf(w, "-- PostgreSQL full dump (no logical slot available)\n\n")
		dumper := postgres.NewDumper(host, port, user, pass, dbName, tlsCfg)
		if err := dumper.OpenContext(ctx); err != nil {
			return err
		}
		defer dumper.Close()
		return dumper.Dump(w, strings.Split(dbName, ","))
	}

	fmt.Fprintf(w, "-- PostgreSQL %s from slot %s\n", strategy, slot)

	written, err := pgChanges(ctx, w, conn, slot, strategy != "differential")
	if err != nil {
		return fmt.Errorf("slot %s: %w", slot, err)
	}
	if written == 0 {
		fmt.Fprintf(w, "-- No changes since last backup\n")
	}
	return nil
}

func IncrementalSpec() registry.IncrementalSpec {
	return registry.IncrementalSpec{
		Engine: "postgres",
		CheckSupport: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return CheckSupport(ctx, host, port, user, pass, dbName, tlsCfg)
		},
		GetPosition: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
			return Position(ctx, host, port, user, pass, dbName, tlsCfg)
		},
		Dump: func(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error {
			return IncrementalDump(ctx, w, host, port, user, pass, dbName, strategy, since, tlsCfg)
		},
		EnsureSlots: func(ctx context.Context, host string, port int, user, pass, dbName string) error {
			return EnsureLogicalSlots(ctx, host, port, user, pass, dbName)
		},
	}
}
func Position(ctx context.Context, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) (string, error) {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "postgres"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := pgx.Connect(ctx, postgres.ConnStr(user, pass, host, port, firstDB, tlsCfg))
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var wal string
	if err := conn.QueryRow(ctx, "SELECT pg_walfile_name(pg_current_wal_lsn())").Scan(&wal); err != nil {
		return "", fmt.Errorf("get WAL: %w", err)
	}
	return strings.TrimSpace(wal), nil
}

func SlotName(dbName, kind string) string {
	name := "dbbackup_" + kind + "_" + dbName
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, name)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}
func init() {
	registry.RegisterIncremental(IncrementalSpec())
}

func nextFieldAt(s string, pos int) bool {
	if pos >= len(s) {
		return false
	}
	if !((s[pos] >= 'a' && s[pos] <= 'z') || (s[pos] >= 'A' && s[pos] <= 'Z')) {
		return false
	}
	for j := pos + 1; j < len(s); j++ {
		c := s[j]
		if c == '[' {
			return true
		}
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return false
}

func parsePGFields(s string) ([]string, []string) {
	var names, values []string
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) {
			break
		}
		ci := strings.Index(s[i:], "[")
		if ci < 0 {
			break
		}
		ci += i
		name := s[i:ci]
		i = ci

		j := strings.Index(s[i:], "]")
		if j < 0 {
			break
		}
		i += j + 1

		if i < len(s) && s[i] == ':' {
			i++
		}
		valStart := i
		for i < len(s) {
			if s[i] == '\'' {
				i++
				for i < len(s) {
					if s[i] == '\'' {
						if i+1 < len(s) && s[i+1] == '\'' {
							i += 2
							continue
						}
						i++
						break
					}
					i++
				}
				continue
			}
			if s[i] == ' ' && nextFieldAt(s, i+1) {
				break
			}
			i++
		}
		val := s[valStart:i]

		names = append(names, name)
		values = append(values, val)
	}
	return names, values
}

func escapePGIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func escapeQualifiedPGIdent(qualified string) string {
	parts := strings.Split(qualified, ".")
	for i, p := range parts {
		parts[i] = escapePGIdent(p)
	}
	return strings.Join(parts, ".")
}

func pgChangeToSQL(ctx context.Context, data string, pks map[string][]string, conn *pgx.Conn) (string, bool) {
	rest, ok := strings.CutPrefix(data, "table ")
	if !ok {
		return "", false
	}
	parts := strings.SplitN(rest, ": ", 3)
	if len(parts) < 3 {
		return "", false
	}
	table := parts[0]
	op := parts[1]
	cols := parts[2]

	names, values := parsePGFields(cols)
	if len(names) == 0 {
		return "", false
	}

	qtable := escapeQualifiedPGIdent(table)
	qnames := make([]string, len(names))
	for i, n := range names {
		qnames[i] = escapePGIdent(n)
	}
	qcols := strings.Join(qnames, ", ")
	switch op {
	case "INSERT":
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);",
			qtable, qcols, strings.Join(values, ", ")), true
	case "UPDATE":
		pk := pgPK(ctx, conn, table, pks)
		var sets []string
		var conds []string
		for i, n := range names {
			if common.ContainsString(pk, n) {
				conds = append(conds, fmt.Sprintf("%s = %s", qnames[i], values[i]))
			} else {
				sets = append(sets, fmt.Sprintf("%s = %s", qnames[i], values[i]))
			}
		}
		if len(conds) == 0 {
			return "", false
		}
		return fmt.Sprintf("UPDATE %s SET %s WHERE %s;",
			qtable, strings.Join(sets, ", "), strings.Join(conds, " AND ")), true
	case "DELETE":
		var conds []string
		for i := range names {
			conds = append(conds, fmt.Sprintf("%s = %s", qnames[i], values[i]))
		}
		return fmt.Sprintf("DELETE FROM %s WHERE %s;", qtable, strings.Join(conds, " AND ")), true
	}
	return "", false
}

func pgChanges(ctx context.Context, w io.Writer, conn *pgx.Conn, slot string, advance bool) (int, error) {
	fn := "pg_logical_slot_peek_changes"
	if advance {
		fn = "pg_logical_slot_get_changes"
	}
	query := fmt.Sprintf("SELECT data FROM %s($1, NULL, NULL)", fn)
	rows, err := conn.Query(ctx, query, slot)
	if err != nil {
		return 0, err
	}

	var lines []string
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			rows.Close()
			return 0, err
		}
		lines = append(lines, data)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	pks := map[string][]string{}
	var written int
	for _, data := range lines {
		if line, ok := pgChangeToSQL(ctx, data, pks, conn); ok {
			fmt.Fprintf(w, "%s\n", line)
			written++
		}
	}
	return written, nil
}

func pgPK(ctx context.Context, conn *pgx.Conn, table string, pks map[string][]string) []string {
	if cached, ok := pks[table]; ok {
		return cached
	}
	var cols []string
	rows, err := conn.Query(ctx,
		`SELECT a.attname
		   FROM pg_index i
		   JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		  WHERE i.indrelid = $1::regclass AND i.indisprimary
		  ORDER BY array_position(i.indkey, a.attnum)`, table)
	if err != nil {
	} else {
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				cols = append(cols, c)
			}
		}
		rows.Close()
	}
	pks[table] = cols
	return cols
}

func slotExists(ctx context.Context, conn *pgx.Conn, slot string) (bool, error) {
	var exists bool
	err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", slot).Scan(&exists)
	return exists, err
}

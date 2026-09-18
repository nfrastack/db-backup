// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

func dollarTagOpen(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == '_' ||
			(s[j] >= 'a' && s[j] <= 'z') ||
			(s[j] >= 'A' && s[j] <= 'Z') ||
			(s[j] >= '0' && s[j] <= '9')) {
			j++
		}
		if j < len(s) && s[j] == '$' && j > i+1 {
			return s[i : j+1]
		}
	}
	return ""
}

func execStmtGroup(ctx context.Context, conn *pgx.Conn, stmt *strings.Builder) error {
	if stmt.Len() == 0 {
		return nil
	}
	for _, s := range splitStatements(stmt.String()) {
		var clean strings.Builder
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "--") {
				continue
			}
			clean.WriteString(l)
			clean.WriteByte('\n')
		}
		if strings.TrimSpace(clean.String()) == "" {
			continue
		}
		if _, err := conn.Exec(ctx, clean.String()); err != nil {
			short := clean.String()
			if len(short) > 80 {
				short = short[:80]
			}
			return fmt.Errorf("exec: %w (%.80s)", err, short)
		}
	}
	return nil
}

func guardSession(ctx context.Context, conn *pgx.Conn) {
	if _, err := conn.Exec(ctx,
		"SET statement_timeout = 0; SET lock_timeout = 0; SET idle_in_transaction_session_timeout = 0"); err != nil {
		log.Trace("postgres", "session guard unavailable", "error", err.Error())
	}
}

func ListDatabases(host string, port int, user, pass string, tlsCfg *config.TLSConfig) ([]string, error) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ConnStr(user, pass, host, port, "postgres", tlsCfg))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	guardSession(ctx, conn)

	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	var dbs []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		if isPgSystemDB(name) {
			continue
		}
		dbs = append(dbs, name)
	}
	return dbs, rows.Err()
}

func Maintain(host string, port int, user, pass, dbName string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	return nil, nil
}

func pgExecCopy(ctx context.Context, conn *pgx.Conn, header string, data *strings.Builder) error {
	if header == "" {
		return nil
	}
	payload := data.String()
	if payload != "" && !strings.HasSuffix(payload, "\n") {
		payload += "\n"
	}
	dr := strings.NewReader(payload)
	if _, err := conn.PgConn().CopyFrom(ctx, dr, header); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

func pgRestoreStream(ctx context.Context, conn *pgx.Conn, r io.Reader) error {
	br := bufio.NewReader(r)

	var stmt strings.Builder
	inCopy := false
	var copyHeader string
	var copyBuf strings.Builder
	var tag string

	for {
		raw, readErr := br.ReadString('\n')
		line := strings.TrimSuffix(raw, "\n")
		line = strings.TrimSuffix(line, "\r")

		if len(raw) > 0 {
			if inCopy {
				if line == "\\." {
					inCopy = false
					if err := pgExecCopy(ctx, conn, copyHeader, &copyBuf); err != nil {
						return err
					}
					copyBuf.Reset()
					copyHeader = ""
					stmt.Reset()
				} else {
					if copyBuf.Len() > 0 {
						copyBuf.WriteByte('\n')
					}
					copyBuf.WriteString(line)
				}
			} else {
				nextTag := updateDollarTag(line, tag)
				if tag == "" && strings.HasPrefix(line, "COPY ") && strings.Contains(line, " FROM stdin;") {
					if stmt.Len() > 0 {
						if err := execStmtGroup(ctx, conn, &stmt); err != nil {
							return err
						}
						stmt.Reset()
					}
					copyHeader = strings.TrimSuffix(line, ";")
					inCopy = true
				} else {
					tag = nextTag

					stmt.WriteString(line)
					stmt.WriteByte('\n')

					if tag == "" && strings.HasSuffix(strings.TrimSpace(line), ";") {
						if err := execStmtGroup(ctx, conn, &stmt); err != nil {
							return err
						}
						stmt.Reset()
					}
				}
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("read dump: %w", readErr)
		}
	}

	return execStmtGroup(ctx, conn, &stmt)
}

func Restore(r io.Reader, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "postgres"
	}

	ctx := context.Background()
	if common.CreateDBOnRestore && firstDB != "postgres" && firstDB != "template1" && firstDB != "template0" {
		bootConn, err := pgx.Connect(ctx, ConnStr(user, pass, host, port, "postgres", tlsCfg))
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		guardSession(ctx, bootConn)
		var exists bool
		if err := bootConn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", firstDB).Scan(&exists); err != nil {
			bootConn.Close(ctx)
			return fmt.Errorf("check database: %w", err)
		}
		if !exists {
			if _, err := bootConn.Exec(ctx, "CREATE DATABASE "+quotePGIdent(firstDB)); err != nil {
				bootConn.Close(ctx)
				return fmt.Errorf("create db: %w", err)
			}
			log.Info("postgres", "database created", "database", firstDB)
		}
		bootConn.Close(ctx)
	}

	conn, err := pgx.Connect(ctx, ConnStr(user, pass, host, port, firstDB, tlsCfg))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	guardSession(ctx, conn)

	return pgRestoreStream(ctx, conn, r)
}

func splitStatements(src string) []string {
	var out []string
	var cur strings.Builder
	var tag string
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, line := range strings.Split(src, "\n") {
		cur.WriteString(line)
		cur.WriteByte('\n')
		tag = updateDollarTag(line, tag)
		if tag == "" && strings.HasSuffix(strings.TrimSpace(line), ";") {
			flush()
		}
	}
	flush()
	return out
}

func updateDollarTag(line, tag string) string {
	rest := line
	for {
		if tag == "" {
			open := dollarTagOpen(rest)
			if open == "" {
				return ""
			}
			after := rest[strings.Index(rest, open)+len(open):]
			if idx := strings.Index(after, open); idx >= 0 {
				rest = after[idx+len(open):]
				continue
			}
			return open
		}
		if idx := strings.Index(rest, tag); idx >= 0 {
			rest = rest[idx+len(tag):]
			tag = ""
			continue
		}
		return tag
	}
}

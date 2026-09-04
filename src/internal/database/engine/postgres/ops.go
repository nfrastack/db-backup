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
)

func execStmtGroup(ctx context.Context, conn *pgx.Conn, stmt *strings.Builder) error {
	if stmt.Len() == 0 {
		return nil
	}
	var clean strings.Builder
	for _, l := range strings.Split(stmt.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "--") {
			continue
		}
		clean.WriteString(l)
		clean.WriteByte('\n')
	}
	if strings.TrimSpace(clean.String()) == "" {
		return nil
	}

	var cur strings.Builder
	for _, l := range strings.Split(clean.String(), "\n") {
		cur.WriteString(l)
		cur.WriteByte('\n')
		if strings.HasSuffix(strings.TrimSpace(l), ";") {
			if s := strings.TrimSpace(cur.String()); s != "" {
				if _, err := conn.Exec(ctx, s); err != nil {
					if len(s) > 80 {
						s = s[:80]
					}
					return fmt.Errorf("exec: %w (%.80s)", err, s)
				}
			}
			cur.Reset()
		}
	}
	return nil
}

func ListDatabases(host string, port int, user, pass string, tlsCfg *config.TLSConfig) ([]string, error) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ConnStr(user, pass, host, port, "postgres", tlsCfg))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

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

func Restore(r io.Reader, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "postgres"
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ConnStr(user, pass, host, port, firstDB, tlsCfg))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	if firstDB != "postgres" && firstDB != "template1" && firstDB != "template0" {
		bootstrap := ConnStr(user, pass, host, port, "postgres", tlsCfg)
		if bootConn, err := pgx.Connect(ctx, bootstrap); err == nil {
			var exists bool
			bootConn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", firstDB).Scan(&exists)
			if !exists {
				sanitized := strings.ReplaceAll(firstDB, "'", "''")
				bootConn.Exec(ctx, "CREATE DATABASE "+sanitized)
			}
			bootConn.Close(ctx)
		}
	}

	return pgRestoreStream(ctx, conn, r)
}

func pgExecCopy(ctx context.Context, conn *pgx.Conn, header string, data *strings.Builder) error {
	if header == "" {
		return nil
	}
	payload := data.String()
	if !strings.HasSuffix(payload, "\n") {
		payload += "\n"
	}
	dr := strings.NewReader(payload)
	if _, err := conn.PgConn().CopyFrom(ctx, dr, header); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

func pgRestoreStream(ctx context.Context, conn *pgx.Conn, r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var stmt strings.Builder
	inCopy := false
	var copyHeader string
	var copyBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

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
			continue
		}

		if strings.HasPrefix(line, "COPY ") && strings.Contains(line, " FROM stdin;") {
			if stmt.Len() > 0 {
				if s := strings.TrimSpace(stmt.String()); s != "" {
					if _, err := conn.Exec(ctx, s); err != nil {
						return fmt.Errorf("exec: %w (%.80s)", err, s)
					}
				}
				stmt.Reset()
			}
			copyHeader = strings.TrimSuffix(line, ";")
			inCopy = true
			continue
		}

		stmt.WriteString(line)
		stmt.WriteByte('\n')

		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			if err := execStmtGroup(ctx, conn, &stmt); err != nil {
				return err
			}
			stmt.Reset()
		}
	}

	return execStmtGroup(ctx, conn, &stmt)
}

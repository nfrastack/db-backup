// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/engine/mysql"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

type binlogEvent struct {
	LogPos uint32
	Event  string
	Info   string
}

func CheckSupport(ctx context.Context, host string, port int, user, pass string, tlsCfg *config.TLSConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := sql.Open("mysql", mysql.ConnectDSN(user, pass, host, port, "charset=utf8mb4", mysql.TLSNameFor(tlsCfg)))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	var on string
	if err := db.QueryRow("SELECT @@log_bin").Scan(&on); err != nil {
		return fmt.Errorf("log_bin: %w", err)
	}
	if on != "1" && !strings.EqualFold(on, "ON") {
		return fmt.Errorf("binary logging is disabled (log_bin=%q) - enable log_bin with binlog_format=STATEMENT and server-id", on)
	}

	var format string
	if err := db.QueryRow("SELECT @@binlog_format").Scan(&format); err == nil {
		format = strings.ToUpper(format)
		if format == "ROW" {
			return fmt.Errorf("binlog_format=ROW cannot be replayed as SQL - set binlog_format=STATEMENT")
		}
	}
	return nil
}

func IncrementalDump(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since string, tlsCfg *config.TLSConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := sql.Open("mysql", mysql.ConnectDSN(user, pass, host, port,
		"charset=utf8mb4&multiStatements=true&interpolateParams=true", mysql.TLSNameFor(tlsCfg)))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	if _, err := db.Exec("FLUSH LOGS"); err != nil {
		return fmt.Errorf("flush logs: %w", err)
	}

	if since == "" {
		dumper := mysql.NewDumper(host, port, user, pass, tlsCfg)
		if err := dumper.Open(); err != nil {
			return err
		}
		defer dumper.Close()
		return dumper.Dump(w, strings.Split(dbName, ","))
	}

	parts := strings.SplitN(since, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid position: %s", since)
	}
	startFile := parts[0]
	startPos, _ := strconv.ParseUint(parts[1], 10, 32)

	label := "incremental"
	if strategy == "differential" {
		label = "differential"
	}
	fmt.Fprintf(w, "-- MySQL %s from %s\n", label, since)

	files, err := listBinlogs(db)
	if err != nil {
		return fmt.Errorf("list binlogs: %w", err)
	}

	startIdx := -1
	for i, f := range files {
		if f == startFile {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		fmt.Fprintf(w, "-- binlog %s no longer available, full dump fallback\n\n", startFile)
		dumper := mysql.NewDumper(host, port, user, pass, tlsCfg)
		if err := dumper.Open(); err != nil {
			return err
		}
		defer dumper.Close()
		return dumper.Dump(w, strings.Split(dbName, ","))
	}

	var written bool
	firstFile := true
	for i := startIdx; i < len(files); i++ {
		var from uint32
		if firstFile {
			from = uint32(startPos)
			firstFile = false
		}
		events, err := binlogEventsIn(db, files[i], from)
		if err != nil {
			fmt.Fprintf(w, "-- binlog read failed (%v), full dump fallback\n\n", err)
			dumper := mysql.NewDumper(host, port, user, pass, tlsCfg)
			if err := dumper.OpenContext(ctx); err != nil {
				return err
			}
			defer dumper.Close()
			return dumper.Dump(w, strings.Split(dbName, ","))
		}

		if i > startIdx {
			fmt.Fprintf(w, "-- binlog %s\n", files[i])
		}

		for _, ev := range events {
			if ev.Event == "Query" && ev.Info != "" {
				if strings.Contains(ev.Info, "FLUSH LOGS") || strings.HasPrefix(ev.Info, "BEGIN") {
					continue
				}
				fmt.Fprintf(w, "%s;\n", ev.Info)
				written = true
			}
		}
	}

	if !written {
		fmt.Fprintf(w, "-- No new events since %s\n", since)
	}

	return nil
}

func IncrementalSpec() registry.IncrementalSpec {
	return registry.IncrementalSpec{
		Engine: "mysql",
		CheckSupport: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return CheckSupport(ctx, host, port, user, pass, tlsCfg)
		},
		GetPosition: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
			return Position(ctx, host, port, user, pass, tlsCfg)
		},
		Dump: func(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error {
			return IncrementalDump(ctx, w, host, port, user, pass, dbName, strategy, since, tlsCfg)
		},
	}
}
func Position(ctx context.Context, host string, port int, user, pass string, tlsCfg *config.TLSConfig) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	db, err := sql.Open("mysql", mysql.ConnectDSN(user, pass, host, port, "charset=utf8mb4", mysql.TLSNameFor(tlsCfg)))
	if err != nil {
		return "", err
	}
	defer db.Close()

	var logFile, position string

	row := db.QueryRow("SHOW BINARY LOG STATUS")
	var d1, d2, d3 string
	if err := row.Scan(&logFile, &position, &d1, &d2, &d3); err != nil {
		row = db.QueryRow("SHOW BINARY LOG STATUS")
		if err2 := row.Scan(&logFile, &position, &d1, &d2); err2 != nil {
			row = db.QueryRow("SHOW MASTER STATUS")
			if err3 := row.Scan(&logFile, &position, &d1, &d2); err3 != nil {
				return "", fmt.Errorf("get binlog position: %w", err3)
			}
		}
	}

	if position == "" {
		position = "4"
	}
	return logFile + ":" + position, nil
}

func binlogEventsIn(db *sql.DB, file string, fromPos uint32) ([]binlogEvent, error) {
	query := "SHOW BINLOG EVENTS IN '" + strings.ReplaceAll(file, "'", "''") + "'"
	if fromPos > 0 {
		query += fmt.Sprintf(" FROM %d", fromPos)
	}
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	var events []binlogEvent
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		var ev binlogEvent
		for i, v := range vals {
			if i >= len(cols) {
				continue
			}
			str := func() string {
				switch s := v.(type) {
				case string:
					return s
				case []byte:
					return string(s)
				}
				return ""
			}()
			switch cols[i] {
			case "Log_pos", "Pos":
				if n, ok := v.(int64); ok {
					ev.LogPos = uint32(n)
				} else if s := str; s != "" {
					if n, err := strconv.ParseUint(s, 10, 32); err == nil {
						ev.LogPos = uint32(n)
					}
				}
			case "Event_type":
				ev.Event = str
			case "Info":
				ev.Info = str
			}
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}
func init() {
	registry.RegisterIncremental(IncrementalSpec())
}

func listBinlogs(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SHOW BINARY LOGS")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		if len(vals) > 0 {
			switch s := vals[0].(type) {
			case string:
				names = append(names, s)
			case []byte:
				names = append(names, string(s))
			}
		}
	}
	return names, rows.Err()
}

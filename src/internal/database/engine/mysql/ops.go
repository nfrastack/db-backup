// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

var (
	reDefiner    = regexp.MustCompile("(?i)\\s*DEFINER\\s*=\\s*(`[^`]*`|'[^']*'|\"[^\"]*\"|[A-Za-z0-9_.$-]+)(\\s*@\\s*(`[^`]*`|'[^']*'|\"[^\"]*\"|[A-Za-z0-9_.$%-]+))?")
	reInsertQual = regexp.MustCompile("(?i)INSERT\\s+INTO\\s+`[^`]+`\\.")
	reOtherQual  = regexp.MustCompile("(?i)(FROM|UPDATE|DELETE\\s+FROM|TRUNCATE\\s+TABLE|RENAME\\s+TABLE|DROP\\s+TABLE|ALTER\\s+TABLE|CREATE\\s+TABLE)\\s+`[^`]+`\\.")
	rePlainQual  = regexp.MustCompile("(?i)(INSERT\\s+INTO|UPDATE|DELETE\\s+FROM|TRUNCATE\\s+TABLE|RENAME\\s+TABLE|DROP\\s+TABLE|ALTER\\s+TABLE|CREATE\\s+TABLE)\\s+([A-Za-z0-9_]+)\\.")
	reUseSrc     = regexp.MustCompile("(?i)USE\\s+`([^`]+)`")
	reUseStmt    = regexp.MustCompile("(?i)USE\\s+`[^`]+`\\s*;")
)

func ListDatabases(host string, port int, user, pass string, tlsCfg *config.TLSConfig) ([]string, error) {
	db, err := sql.Open("mysql", ConnectDSN(user, pass, host, port, "charset=utf8mb4", TLSNameFor(tlsCfg)))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	rows, err := db.Query("SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("show databases: %w", err)
	}
	defer rows.Close()

	var dbs []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		if !isSystemDB(name) {
			dbs = append(dbs, name)
		}
	}
	return dbs, rows.Err()
}

func Maintain(host string, port int, user, pass, dbName string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	db, err := sql.Open("mysql", ConnectDSN(user, pass, host, port, "charset=utf8mb4&multiStatements=true", TLSNameFor(tlsCfg)))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}

	var results []common.OpResult
	names := common.DBNamesList(dbName)

	for _, n := range names {
		if _, err := db.Exec("USE `" + n + "`"); err != nil {
			continue
		}

		if common.Enabled("check_tables", cfg) {
			if r := runMySQLOp(db, "CHECK TABLE", n); r != nil {
				results = append(results, *r)
			}
		}
	}
	return results, nil
}

func Restore(r io.Reader, host string, port int, user, pass, dbName string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	db, err := sql.Open("mysql", ConnectDSN(user, pass, host, port,
		"charset=utf8mb4&multiStatements=true&interpolateParams=true", TLSNameFor(tlsCfg)))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	if _, err := db.Exec("SET SESSION BINLOG_FORMAT='ROW'"); err != nil {
		log.Debug("mysql", "row binlogging unavailable", "error", err.Error())
	}

	if firstDB != "" && common.CreateDBOnRestore {
		if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS `" + firstDB + "`"); err != nil {
			return fmt.Errorf("create db: %w", err)
		}
		log.Info("mysql", "database created", "database", firstDB)
	}

	target := ""
	if firstDB != "" {
		if targets := strings.Split(firstDB, ","); len(targets) == 1 && targets[0] != "" {
			target = targets[0]
		}
	}

	var reUseTarget *regexp.Regexp
	if target != "" {
		reUseTarget = regexp.MustCompile("(?i)USE\\s+`" + regexp.QuoteMeta(target) + "`\\s*;")
	}

	sources := map[string]bool{}
	sp := newStmtSplitter()
	definerCount := 0
	useEnsured := false
	execStmt := func(raw string) error {
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		out, n := rewriteStatement(raw, target, sources)
		definerCount += n
		isTargetUse := reUseTarget != nil && reUseTarget.MatchString(out)
		if reUseTarget != nil && !useEnsured && !isTargetUse {
			if _, err := db.Exec("USE `" + target + "`"); err != nil {
				return fmt.Errorf("exec: %w", err)
			}
			useEnsured = true
		}
		if _, err := db.Exec(out); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
		if isTargetUse {
			useEnsured = true
		}
		return nil
	}

	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			noline := strings.TrimSuffix(line, "\n")
			noteMarkerSource(noline, target, sources)
			for _, done := range sp.pushLine(noline) {
				if err := execStmt(done); err != nil {
					return err
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read dump: %w", err)
		}
	}
	if s := sp.flush(); s != "" {
		if err := execStmt(s); err != nil {
			return err
		}
	}
	if definerCount > 0 {
		log.Debug("mysql", "definers stripped", "count", definerCount)
	}
	return nil
}

func insertHeaderEnd(stmt string) int {
	upper := strings.ToUpper(stmt)
	for i := 0; i+6 <= len(upper); i++ {
		if upper[i:i+6] != "VALUES" {
			continue
		}
		if i > 0 && isWordChar(upper[i-1]) {
			continue
		}
		if i+6 < len(upper) && isWordChar(upper[i+6]) {
			continue
		}
		return i
	}
	return -1
}

func isInsertStmt(stmt string) bool {
	s := stmt
	for {
		t := strings.TrimLeft(s, " \t\r\n")
		if strings.HasPrefix(t, "--") || strings.HasPrefix(t, "#") {
			i := strings.IndexByte(t, '\n')
			if i < 0 {
				return false
			}
			s = t[i+1:]
			continue
		}
		if strings.HasPrefix(t, "/*") {
			i := strings.Index(t[2:], "*/")
			if i < 0 {
				return false
			}
			s = t[2+i+2:]
			continue
		}
		return len(t) >= 6 && strings.ToUpper(t[:6]) == "INSERT" &&
			(len(t) == 6 || !isWordChar(t[6]))
	}
}

func isWordChar(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

func listMySQLTables(db *sql.DB, dbName string) ([]string, error) {
	rows, err := db.Query("SHOW TABLES FROM `" + dbName + "`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		rows.Scan(&t)
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func noteMarkerSource(line, target string, sources map[string]bool) {
	trimmed := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(trimmed, "-- Database:")
	if !ok {
		return
	}
	if fields := strings.Fields(strings.TrimSpace(rest)); len(fields) == 1 {
		if fields[0] != "" && fields[0] != target {
			sources[fields[0]] = true
		}
	}
}

func rewriteStatement(stmt, target string, sources map[string]bool) (string, int) {
	isInsert := isInsertStmt(stmt)
	scope, tail := stmt, ""
	if isInsert && target != "" {
		if idx := insertHeaderEnd(stmt); idx >= 0 {
			scope, tail = stmt[:idx], stmt[idx:]
		}
	}
	stripped := 0
	if target != "" {
		if m := reUseSrc.FindStringSubmatch(scope); m != nil {
			if m[1] != "" && m[1] != target {
				sources[m[1]] = true
			}
		}
		scope = reUseStmt.ReplaceAllString(scope, "USE `"+target+"`;")
		scope = reInsertQual.ReplaceAllString(scope, "INSERT INTO `"+target+"`.")
		scope = reOtherQual.ReplaceAllString(scope, "$1 `"+target+"`.")
		scope = rePlainQual.ReplaceAllString(scope, "${1} "+target+".")
		for src := range sources {
			if src != "" && src != target {
				scope = strings.ReplaceAll(scope, "`"+src+"`.", "`"+target+"`.")
			}
		}
	}
	if !isInsert {
		if found := reDefiner.FindAllString(scope, -1); len(found) > 0 {
			stripped = len(found)
			scope = reDefiner.ReplaceAllString(scope, "")
		}
	}
	return scope + tail, stripped
}

func runMySQLOp(db *sql.DB, op, dbName string) *common.OpResult {
	r, start := common.StartOp(op)
	tables, err := listMySQLTables(db, dbName)
	if err != nil {
		res := common.FinishOp(&r, start)
		res.Status = "ERROR"
		res.Detail = fmt.Sprintf("list tables: %v", err)
		return &res
	}
	var errs []string
	for _, t := range tables {
		if _, err := db.Exec(fmt.Sprintf("%s `%s`.`%s`", op, dbName, t)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t, err))
		}
	}
	status := "OK"
	detail := fmt.Sprintf("%d tables", len(tables))
	if len(errs) > 0 {
		status = "ERROR"
		detail = strings.Join(errs, "; ")
	}
	res := common.FinishOp(&r, start)
	res.Status = status
	res.Detail = detail
	return &res
}

func splitSQLStatements(data string) []string {
	var out []string
	sp := newStmtSplitter()
	for _, line := range strings.Split(data, "\n") {
		out = append(out, sp.pushLine(line)...)
	}
	if s := sp.flush(); s != "" {
		out = append(out, s)
	}
	return out
}

type stmtSplitter struct {
	buf                            strings.Builder
	delimiter                      string
	inSingle, inDouble, inBacktick bool
	inLineComment, inBlockComment  bool
}

func (sp *stmtSplitter) endsStatement() bool {
	if sp.delimiter == "" || !sp.neutral() || sp.inLineComment {
		return false
	}
	s := strings.TrimSpace(sp.buf.String())
	if !strings.HasSuffix(s, sp.delimiter) {
		return false
	}
	return true
}

func (sp *stmtSplitter) flush() string {
	s := strings.TrimSpace(sp.buf.String())
	sp.buf.Reset()
	if s == "" {
		return ""
	}
	if sp.delimiter != "" && strings.HasSuffix(s, sp.delimiter) {
		s = strings.TrimSpace(strings.TrimSuffix(s, sp.delimiter))
	}
	return s
}

func (sp *stmtSplitter) neutral() bool {
	return !sp.inSingle && !sp.inDouble && !sp.inBacktick && !sp.inBlockComment
}

func newStmtSplitter() *stmtSplitter {
	return &stmtSplitter{delimiter: ";"}
}

func (sp *stmtSplitter) pushLine(line string) []string {
	var done []string
	trimmed := strings.TrimSpace(line)
	upper := strings.ToUpper(trimmed)
	if sp.neutral() && !sp.inLineComment && strings.HasPrefix(upper, "DELIMITER ") {
		if s := sp.flush(); s != "" {
			done = append(done, s)
		}
		sp.delimiter = strings.TrimSpace(trimmed[len("DELIMITER "):])
		return done
	}
	if trimmed == "" && sp.buf.Len() == 0 {
		sp.inLineComment = false
		return done
	}
	sp.scan(line)
	sp.buf.WriteString(line)
	sp.buf.WriteString("\n")
	sp.inLineComment = false
	if sp.endsStatement() {
		if s := sp.flush(); s != "" {
			done = append(done, s)
		}
	}
	return done
}

func (sp *stmtSplitter) scan(line string) {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if sp.inLineComment {
			continue
		}
		if sp.inBlockComment {
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				sp.inBlockComment = false
				i++
			}
			continue
		}
		if sp.inSingle {
			if c == '\\' {
				i++
			} else if c == '\'' {
				if i+1 < len(line) && line[i+1] == '\'' {
					i++
				} else {
					sp.inSingle = false
				}
			}
			continue
		}
		if sp.inDouble {
			if c == '\\' {
				i++
			} else if c == '"' {
				sp.inDouble = false
			}
			continue
		}
		if sp.inBacktick {
			if c == '`' {
				sp.inBacktick = false
			}
			continue
		}
		switch {
		case c == '\'':
			sp.inSingle = true
		case c == '"':
			sp.inDouble = true
		case c == '`':
			sp.inBacktick = true
		case c == '#' || (c == '-' && i+1 < len(line) && line[i+1] == '-' &&
			(i+2 >= len(line) || line[i+2] == ' ' || line[i+2] == '\t' || line[i+2] == '\r')):
			sp.inLineComment = true
		case c == '/' && i+1 < len(line) && line[i+1] == '*':
			sp.inBlockComment = true
			i++
		}
	}
}

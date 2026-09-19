// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
)

var hostSanitizer = strings.NewReplacer(
	".", "_",
	"/", "_",
	"\\", "_",
	":", "_",
	"@", "_",
	" ", "_",
	"-", "_",
)

func createLatestSymlink(job config.JobConfig, dbName, storagePath, filename string) {
	if job.Backup == nil || job.Backup.CreateLatest == nil || !*job.Backup.CreateLatest {
		return
	}
	if !strings.EqualFold(job.Storage.Backend, "filesystem") {
		JLog(log.LevelWarn, job, "create_latest symlink only supported for filesystem storage",
			"status", "warn", "backend", job.Storage.Backend)
		return
	}
	dbTok, hostTok := defaultFilenameTokens(job, dbName)
	name := strings.NewReplacer(",", "_", "/", "_").Replace(dbTok)
	link := filepath.Join(storagePath, "latest-"+joinNonEmpty("_", job.Type, name, hostTok))
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		JLog(log.LevelWarn, job, "create_latest symlink update failed",
			"status", "warn", "link", link, "error", err.Error())
		return
	}
	if err := os.Symlink(filename, link); err != nil {
		JLog(log.LevelWarn, job, "create_latest symlink update failed",
			"status", "warn", "link", link, "error", err.Error())
		return
	}
	JLog(log.LevelDebug, job, "latest symlink updated",
		"status", "debug", "link", link, "target", filename)
}

func dbToken(job config.JobConfig, dbName string) string {
	if strings.EqualFold(job.Type, "sqlite") || strings.EqualFold(job.Type, "sqlite3") {
		base := filepath.Base(dbName)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	if strings.EqualFold(dbName, "__globals__") {
		return "globals"
	}
	if hasAllToken(strings.Split(dbName, ",")) {
		return "all"
	}
	if dbName != "" && !strings.EqualFold(dbName, "ALL") {
		dbName = strings.ReplaceAll(dbName, ",", "_")
		dbName = strings.ReplaceAll(dbName, "-", "_")
	}
	return dbName
}

func dbsToken(job config.JobConfig, dbName string) string {
	names := dbName
	if job.Databases != nil && len(job.Databases.Include) > 0 {
		names = strings.Join(job.Databases.Include, ",")
	}
	tok := strings.NewReplacer(",", "_", "/", "_", "-", "_").Replace(names)
	return guardToken(tok)
}

func defaultFilenameTokens(job config.JobConfig, dbName string) (string, string) {
	if isSQLiteType(job.Type) {
		return sqliteIdentity(dbName, job.Host), ""
	}
	return dbToken(job, dbName), hostSanitizer.Replace(job.Host)
}

func expandFilename(tmpl string, job config.JobConfig, dbName, strat string, now time.Time) string {
	switch strings.ToLower(strat) {
	case "incremental":
		strat = "incr"
	case "differential":
		strat = "diff"
	}
	if tmpl == "" {
		dbTok, hostTok := defaultFilenameTokens(job, dbName)
		ts := now.Format("20060102-150405")
		return joinNonEmpty("-", job.Type, dbTok, hostTok, strat, ts)
	}
	utcNow := now.UTC()
	dbTok, _ := defaultFilenameTokens(job, dbName)
	return strings.NewReplacer(
		"%type%", job.Type,
		"%db%", dbTok,
		"%dbs%", dbsToken(job, dbName),
		"%host_raw%", job.Host,
		"%host%", hostSanitizer.Replace(job.Host),
		"%job%", job.Name,
		"%date%", now.Format("20060102"),
		"%time%", now.Format("150405"),
		"%timestamp%", now.Format("20060102-150405"),
		"%date_utc%", utcNow.Format("20060102"),
		"%time_utc%", utcNow.Format("150405"),
		"%timestamp_utc%", utcNow.Format("20060102-150405"),
		"%strategy%", strat,
		"%%", "%",
	).Replace(tmpl)
}

func guardToken(tok string) string {
	const max = 120
	if len(tok) <= max {
		return tok
	}
	sum := fnv.New32a()
	sum.Write([]byte(tok))
	r := []rune(tok)
	return string(r[:96]) + "-" + fmt.Sprintf("%08x", sum.Sum32())
}

func isSQLiteType(dbType string) bool {
	return strings.EqualFold(dbType, "sqlite") || strings.EqualFold(dbType, "sqlite3")
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func sqliteIdentity(dbName, host string) string {
	effective := dbName
	if effective == "" {
		effective = host
	}
	if effective == "" {
		return ""
	}
	base := filepath.Base(effective)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"
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
	host := hostSanitizer.Replace(job.Host)
	name := strings.NewReplacer(",", "_", "/", "_").Replace(dbToken(job, dbName))
	link := filepath.Join(storagePath, fmt.Sprintf("latest-%s_%s_%s", job.Type, name, host))
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
	if dbName != "" && !strings.EqualFold(dbName, "ALL") {
		dbName = strings.ReplaceAll(dbName, ",", "_")
		dbName = strings.ReplaceAll(dbName, "-", "_")
	}
	return dbName
}

func expandFilename(tmpl string, job config.JobConfig, dbName, strat string, now time.Time) string {
	if tmpl == "" {
		tmpl = "%type%-%db%-%host%-%strategy%-%timestamp%"
	}
	switch strings.ToLower(strat) {
	case "incremental":
		strat = "incr"
	case "differential":
		strat = "diff"
	}
	utcNow := now.UTC()
	return strings.NewReplacer(
		"%type%", job.Type,
		"%db%", dbToken(job, dbName),
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

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
)

var DefaultDumpRetention = 30 * 24 * time.Hour
var (
	dumpDir       string
	dumpRetention = DefaultDumpRetention
)

// return the dump dir directory
func DumpDir() string {
	if dumpDir != "" {
		return dumpDir
	}
	return defaultDumpDir()
}

// dump payload to inspectable copy in format 'stats-YYYYMMDD-HHMMSS.nfrastat' one per report. after completion perform cleanup. retention 0 disables dump writing entirely.
func DumpPayload(payload string, now time.Time) string {
	if payload == "" {
		return ""
	}
	if dumpRetention == 0 {
		return ""
	}
	dir := DumpDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Debug("usage-stats", "payload dump failed", "error", err.Error())
		return ""
	}
	path := filepath.Join(dir, "stats-"+now.Format("20060102-150405")+".nfrastat")
	if err := os.WriteFile(path, []byte(payload+"\n"), 0o644); err != nil {
		log.Debug("usage-stats", "payload dump failed", "error", err.Error())
		return ""
	}
	cleanupOldDumps(dir)
	log.Debug("usage-stats", "payload written for inspection", "file", path)
	return path
}

// dump dir <resolved state dir>/stats if not explicitly set (journal lives in <state dir>/stats/journal)
func SetDumpDir(path string) {
	if path != "" {
		dumpDir = path
	}
}

// set stats dump retention period
func SetDumpRetention(d time.Duration) {
	dumpRetention = d
}

// deletes stats-*.nfrastat dumps older than the retention period. negative retention -1 keeps dumps forever
func cleanupOldDumps(dir string) {
	if dumpRetention <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-dumpRetention)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "stats-") || !strings.HasSuffix(name, ".nfrastat") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func defaultDumpDir() string {
	return filepath.Join(config.ResolveStateDir(""), "stats")
}

// alert when the path doesnt exist and we create it
func dumpDirDisplay() string {
	dir := DumpDir()
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir
	}
	return fmt.Sprintf("%s (created on first usage stats report)", dir)
}

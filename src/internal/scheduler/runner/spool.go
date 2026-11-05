// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/storage"
)

func checkSpoolSpace(job config.JobConfig) {
	info := storage.InspectSpoolFS()
	if info.FreeBytes == 0 && !info.Tmpfs {
		return // unknown filesystem data - nothing useful to say
	}
	dir := storage.SpoolDir()
	free := formatBytes(info.FreeBytes)
	switch {
	case info.Tmpfs && info.FreeBytes < storage.TempLowSpaceFloor:
		JLog(log.LevelWarn, job, "spool directory is on tmpfs with little free space - large backups may exhaust memory and fail",
			"status", "warn", "step", "spool", "dir", dir, "free", free)
	case info.Tmpfs:
		JLog(log.LevelWarn, job, "spool directory is on tmpfs (RAM backed) - very large backups may exhaust memory",
			"status", "warn", "step", "spool", "dir", dir, "free", free)
	case info.FreeBytes < storage.TempLowSpaceFloor:
		JLog(log.LevelWarn, job, "spool directory is low on free space - large backups may fail",
			"status", "warn", "step", "spool", "dir", dir, "free", free)
	}
}

func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v, exp = v/unit, exp+1 {
		div *= unit
	}
	suffix := "KMGT"[exp : exp+1]
	return fmt.Sprintf("%.1f %siB", float64(n)/float64(div), suffix)
}

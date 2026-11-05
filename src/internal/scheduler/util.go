// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
)

func jlog(level log.Level, action string, job config.JobConfig, msg string, fields ...any) {
	fields = append(jobFields(job), fields...)
	var lvlOv log.Level
	if job.LogLevel != "" {
		lvlOv = log.ParseLevel(job.LogLevel)
	}
	var colour *bool
	if job.Colour != nil {
		colour = job.Colour
	}
	log.WithOverridesTo(level, lvlOv, colour, job.LogPath, action, msg, fields...)
}

func jobFields(job config.JobConfig) []any {
	fields := []any{"job", job.Name, "type", jobOperation(job)}
	if job.RunID != "" {
		fields = append(fields, "run_id", job.RunID)
	}
	return fields
}

func (s *Scheduler) jobFinished(job config.JobConfig, jobStart time.Time, steps map[string]time.Duration) bool {
	fields := []any{"status", "finished"}
	if timingsEnabled(job) {
		total := time.Since(jobStart)
		backup := total
		var pairs []any
		for _, sub := range []string{"maintain", "archive", "prune"} {
			if d, ok := steps[sub]; ok {
				backup -= d
				pairs = append(pairs, sub, d)
			}
		}
		pairs = append(pairs, "backup", backup, "total", total)
		fields = append(fields, "timing", timingField(pairs...))
	}
	jlog(log.LevelInfo, "backup", job, "job finished", fields...)
	return false
}

func jobOperation(job config.JobConfig) string {
	if job.MaintenanceCfg != nil {
		return "maintain"
	}
	return "backup"
}

func timingField(pairs ...any) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			continue
		}
		dur, ok := pairs[i+1].(time.Duration)
		if !ok {
			continue
		}
		parts = append(parts, key+"="+roundDur(dur))
	}
	return strings.Join(parts, " ")
}

func timingsEnabled(job config.JobConfig) bool {
	if job.Timings != nil {
		return *job.Timings
	}
	return log.TimingsEnabled()
}

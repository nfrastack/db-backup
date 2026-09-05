// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
)

var maintenanceWarned sync.Map

func (s *Scheduler) execJob(job config.JobConfig) error {
	return s.run(job)
}

func (s *Scheduler) execMaintain(job config.JobConfig) error {
	if job.MaintenanceCfg == nil {
		return fmt.Errorf("maintenance job has no profile")
	}

	port := job.Port
	if port == 0 {
		port = runner.DefaultPort(job.Type)
	}
	dbName := ""
	if job.Databases != nil && len(job.Databases.Include) > 0 {
		dbName = strings.Join(job.Databases.Include, ",")
	}

	mcfg := &database.MaintenanceCfg{
		Optimize:    job.MaintenanceCfg.Optimize,
		Vacuum:      job.MaintenanceCfg.Vacuum,
		Reindex:     job.MaintenanceCfg.Reindex,
		Analyze:     job.MaintenanceCfg.Analyze,
		CheckTables: job.MaintenanceCfg.CheckTables,
		Compact:     job.MaintenanceCfg.Compact,
		MemoryPurge: job.MaintenanceCfg.MemoryPurge,
	}

	if restricted := restrictedMaintenanceOps(job.MaintenanceCfg); len(restricted) > 0 {
		if _, already := maintenanceWarned.LoadOrStore(job.Name, true); !already {
			jlog(log.LevelWarn, "maintain", job, "maintenance op(s) require the Supported edition and were skipped",
				"status", "warn", "skipped_ops", strings.Join(restricted, ","))
		}
	}

	jlog(log.LevelDebug, "maintain", job, "connecting to database",
		"status", "debug", "step", "connect", "target", fmt.Sprintf("%s://%s:%d", job.Type, job.Host, port), "engine", job.Type)
	runner.RunHooks(job, "pre", "maintain", dbName, nil)
	results, err := database.Maintain(job.Type, job.Host, port, job.User, job.Pass, dbName, job.AuthSource, mcfg, job.TLS)
	if err != nil {
		runner.RunHooks(job, "post", "maintain", dbName, map[string]string{"DBB_STATUS": "failed"})
		return err
	}

	ok, failed := 0, 0
	var total time.Duration
	for _, r := range results {
		result := strings.ToLower(r.Status)
		fields := []any{"status", "complete", "op", r.Operation, "result", result, "detail", r.Detail}
		if timingsEnabled(job) {
			fields = append(fields, "timing", timingField("duration", r.Duration))
		}
		switch r.Status {
		case "ERROR":
			failed++
			jlog(log.LevelError, "maintain", job, "maintenance op failed", fields...)
		default:
			ok++
			jlog(log.LevelInfo, "maintain", job, "maintenance op", fields...)
		}
		total += r.Duration
	}

	hookStatus := "ok"
	if failed > 0 {
		hookStatus = "failed"
	}
	runner.RunHooks(job, "post", "maintain", dbName, map[string]string{"DBB_STATUS": hookStatus})

	fields := []any{"status", "complete", "result_ok", ok, "result_error", failed}
	if timingsEnabled(job) {
		fields = append(fields, "timing", timingField("total", total))
	}
	if failed > 0 {
		jlog(log.LevelError, "maintain", job, "maintenance complete with errors", fields...)
	} else {
		jlog(log.LevelInfo, "maintain", job, "maintenance complete", fields...)
	}
	return nil
}

func restrictedMaintenanceOps(cfg *config.MaintenanceConfig) []string {
	if cfg == nil || !communityBuild {
		return nil
	}
	var restricted []string
	add := func(set bool, name string) {
		if set {
			restricted = append(restricted, name)
		}
	}
	if cfg.Optimize != nil {
		add(*cfg.Optimize, "optimize")
	}
	if cfg.Vacuum != nil {
		add(*cfg.Vacuum, "vacuum")
	}
	if cfg.Reindex != nil {
		add(*cfg.Reindex, "reindex")
	}
	if cfg.Analyze != nil {
		add(*cfg.Analyze, "analyze")
	}
	if cfg.Compact != nil {
		add(*cfg.Compact, "compact")
	}
	if cfg.MemoryPurge != nil {
		add(*cfg.MemoryPurge, "memory_purge")
	}
	return restricted
}

func (s *Scheduler) runJob(ctx context.Context, job config.JobConfig, idx int) {
	defer s.wg.Done()

	if job.Connectivity == nil || !job.Connectivity.Enabled {
		jlog(log.LevelWarn, "backup", job, "connectivity check disabled - backing up blindly", "status", "warn")
	}

	scheduleDesc := job.Schedule.Describe()
	var firstWait time.Duration
	useBegin := false
	if job.Schedule != nil {

		if d, ok := job.Schedule.FirstWait(); ok {
			firstWait = d
		} else {
			switch {
			case job.Schedule.Cron != "":
				firstWait = nextJobDuration(job.Schedule)
			case job.Schedule.Interval > 0:
				if d, ok := timeAnchorWait(job.Schedule, time.Now()); ok {
					firstWait = d
				} else {
					firstWait = nextJobDuration(job.Schedule)
				}
			case job.Schedule.Begin != "":
				firstWait = parseBegin(job.Schedule.Begin)
				useBegin = true
			case !job.Schedule.Time.Empty():
				firstWait = nextJobDuration(job.Schedule)
			}
		}
	}

	if firstWait == 0 {
		firstWait = time.Duration(idx) * startupStagger
	}

	job.RunID = newRunID()
	nextTime := time.Now().Add(firstWait)
	log.Info("scheduler", fmt.Sprintf("first run at %s (in %s)",
		nextTime.Format("2006-01-02 15:04:05"), roundDur(firstWait)),
		"job", job.Name, "run_id", job.RunID, "status", "starting", "schedule", scheduleDesc)

	firstRun := true
	var blackoutSkips int
	for {
		var wait time.Duration
		first := firstRun
		if firstRun && job.Schedule != nil {

			if d, ok := job.Schedule.FirstWait(); ok {
				wait = d
			} else if useBegin && job.Schedule.Begin != "" {
				wait = parseBegin(job.Schedule.Begin)
			} else {
				wait = nextJobDuration(job.Schedule)
			}
		} else {
			wait = nextJobDuration(job.Schedule)
		}
		firstRun = false

		if first {
			wait = firstWait
		}

		if wait > 0 {
			if !first {

				job.RunID = newRunID()
				nextTime := time.Now().Add(wait).Format("2006-01-02 15:04:05")
				log.Info("scheduler",
					fmt.Sprintf("sleeping until %s (next run in %s)", nextTime, roundDur(wait)),
					"job", job.Name, "run_id", job.RunID, "status", "waiting", "schedule", scheduleDesc)
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
		}

		dbName := ""
		if job.Databases != nil && len(job.Databases.Include) > 0 {
			dbName = strings.Join(job.Databases.Include, ",")
		}
		dbInfo := fmt.Sprintf("%s://%s/%s", job.Type, job.Host, dbName)

		if job.Schedule != nil && !job.Schedule.DayAllowed(time.Now()) {
			jlog(log.LevelDebug, "scheduler", job, "job skipped", "status", "skipped", "reason", "not scheduled for today", "schedule", scheduleDesc)
			if !job.Schedule.IsRecurring() {
				return
			}
			continue
		}
		if job.Schedule != nil && job.Schedule.Blocked(time.Now()) {
			blackoutSkips++
			if blackoutSkips >= 3 {
				jlog(log.LevelWarn, "scheduler", job, "job blocked by blackout",
					"status", "warn", "reason", "blackout window", "consecutive_skips", blackoutSkips)
			} else {
				jlog(log.LevelInfo, "scheduler", job, "job skipped", "status", "skipped", "reason", "blackout window")
			}
			if !job.Schedule.IsRecurring() {
				return
			}
			continue
		}
		blackoutSkips = 0

		s.runOnceJob(ctx, job, dbInfo)

		if !job.Schedule.IsRecurring() {
			return
		}
	}
}

func (s *Scheduler) runOnceJob(ctx context.Context, job config.JobConfig, target string) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.slots != nil {
		tq := time.Now()
		select {
		case s.slots <- struct{}{}:
			jlog(log.LevelDebug, "scheduler", job, "acquired concurrency slot",
				"status", "starting", "concurrency", cap(s.slots), "running", len(s.slots))
		default:
			jlog(log.LevelInfo, "scheduler", job, "queued - waiting for a free concurrency slot",
				"status", "queued", "concurrency", cap(s.slots), "running", len(s.slots))
			select {
			case s.slots <- struct{}{}:
			case <-ctx.Done():
				jlog(log.LevelInfo, "scheduler", job, "job cancelled while queued",
					"status", "cancelled", "reason", "context cancelled")
				return false
			}
			jlog(log.LevelInfo, "scheduler", job, "acquired concurrency slot",
				"status", "starting", "concurrency", cap(s.slots), "running", len(s.slots),
				"delay", roundDur(time.Since(tq)))
		}
		defer func() { <-s.slots }()
	}
	jobStart := time.Now()

	if job.Backup == nil && job.MaintenanceCfg != nil {
		mFields := []any{"status", "started"}
		if target != "" {
			mFields = append(mFields, "target", target)
		}
		jlog(log.LevelInfo, "maintain", job, "job starting", mFields...)
		if err := s.execMaintain(job); err != nil {
			fields := []any{"status", "failed", "error", err.Error()}
			if timingsEnabled(job) {
				fields = append(fields, "timing", timingField("total", time.Since(jobStart)))
			}
			jlog(log.LevelError, "maintain", job, "job finished", fields...)
			return true
		}
		fields := []any{"status", "finished"}
		if timingsEnabled(job) {
			fields = append(fields, "timing", timingField("total", time.Since(jobStart)))
		}
		jlog(log.LevelInfo, "maintain", job, "job finished", fields...)
		return false
	}

	strategy := "full"
	if job.Backup != nil {
		strategy = job.Backup.EffectiveStrategy()
	}
	bFields := []any{"status", "started", "strategy", strategy}
	if target != "" {
		bFields = append(bFields, "target", target)
	}
	jlog(log.LevelInfo, "backup", job, "job starting", bFields...)
	if err := s.execJob(job); err != nil {
		fields := []any{"status", "failed", "error", err.Error()}
		if timingsEnabled(job) {
			fields = append(fields, "timing", timingField("total", time.Since(jobStart)))
		}
		jlog(log.LevelError, "backup", job, "job finished", fields...)
		return true
	}

	if job.MaintenanceCfg != nil {
		now := time.Now()
		if job.MaintenanceCfg.DueOn(now) {
			mStart := time.Now()
			mFields := []any{"status", "started"}
			if target != "" {
				mFields = append(mFields, "target", target)
			}
			jlog(log.LevelInfo, "maintain", job, "job starting", mFields...)
			if err := s.execMaintain(job); err != nil {
				jlog(log.LevelError, "maintain", job, "job finished",
					"status", "failed", "error", err.Error())
			}
			steps := s.postBackup(job)
			steps["maintain"] = time.Since(mStart)
			return s.jobFinished(job, jobStart, steps)
		}

		steps := s.postBackup(job)
		return s.jobFinished(job, jobStart, steps)
	}

	steps := s.postBackup(job)
	return s.jobFinished(job, jobStart, steps)
}

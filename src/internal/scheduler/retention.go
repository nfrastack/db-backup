// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/storage"
)

func (s *Scheduler) applyArchive(job config.JobConfig) {
	if err := retention.CheckArchive(); err != nil {
		jlog(log.LevelWarn, "archive", job, "archive skipped", "status", "skipped", "stage", "license", "error", err.Error())
		return
	}
	srcSt, err := storageFromConfig(job.Storage)
	if err != nil {
		jlog(log.LevelError, "archive", job, "archive failed", "status", "failed", "stage", "storage", "error", err.Error())
		return
	}
	dstCfg := job.Archive.Storage
	if dstCfg == nil {
		dstCfg = archiveStorageCfg(job)
	}
	dstSt, err := storageFromConfig(dstCfg)
	if err != nil {
		jlog(log.LevelError, "archive", job, "archive failed", "status", "failed", "stage", "storage", "error", err.Error())
		return
	}

	var within time.Duration
	if job.Archive.Within != "" {
		within, err = retention.ParseWithin(job.Archive.Within)
		if err != nil {
			jlog(log.LevelError, "archive", job, "archive failed", "status", "failed", "stage", "within", "error", err.Error())
			return
		}
	}

	acfg := &retention.ArchiveConfig{
		Last:   job.Archive.Last,
		Within: within,
		Src:    srcSt,
		Dst:    dstSt,
	}

	start := time.Now()
	moved, candidates, err := retention.RunArchive(acfg)
	if err != nil {
		jlog(log.LevelError, "archive", job, "archive failed", "status", "failed", "stage", "archive", "error", err.Error())
		return
	}
	dstPath := job.Archive.Path
	if dstPath == "" {
		dstPath = dstCfg.Path
	}
	if len(candidates) > 0 {
		jlog(log.LevelDebug, "archive", job, "archive candidates", "status", "debug",
			"target", dstPath, "moving", strings.Join(candidates, ", "))
	}
	if moved == 0 {
		jlog(log.LevelInfo, "archive", job, "nothing to archive", "status", "complete")
		return
	}
	if s.activity != nil {
		s.activity(job.Type, "archive", moved)
	}
	fields := []any{"status", "complete", "moved", moved, "target", dstPath}
	if timingsEnabled(job) {
		fields = append(fields, "timing", timingField("total", time.Since(start)))
	}
	jlog(log.LevelInfo, "archive", job, "archive complete", fields...)
}

func (s *Scheduler) applyPrune(job config.JobConfig, stCfg *config.StorageConfig, ret *config.RetentionConfig) {
	policy := retentionPolicy(ret)
	if !policy.Valid() {
		return
	}
	if err := retention.CheckPrune(policy); err != nil {
		jlog(log.LevelWarn, "prune", job, "GFS retention tiers unavailable - applying last/within only", "status", "warn", "stage", "license", "error", err.Error())
		policy.StripTiers()
		if !policy.Valid() {
			jlog(log.LevelInfo, "prune", job, "prune skipped", "status", "skipped", "stage", "license")
			return
		}
	}
	st, err := storageFromConfig(stCfg)
	if err != nil {
		jlog(log.LevelError, "prune", job, "prune failed", "status", "failed", "stage", "storage", "error", err.Error())
		return
	}
	tList := time.Now()
	backups, err := retention.ListBackupsWithSidecars(st, "")
	listDur := time.Since(tList)
	if err != nil {
		jlog(log.LevelError, "prune", job, "prune failed", "status", "failed", "stage", "list", "error", err.Error())
		return
	}
	tApply := time.Now()
	toDelete := retention.ApplyRetention(backups, policy)
	applyDur := time.Since(tApply)
	if len(toDelete) == 0 {
		return
	}

	policyDesc := policy.Describe()
	jlog(log.LevelDebug, "prune", job, "prune candidates", "status", "debug",
		"policy", policyDesc, "deleting", strings.Join(toDelete, ", "))
	startFields := []any{"status", "starting", "deleted", len(toDelete), "policy", policyDesc}
	if timingsEnabled(job) {
		startFields = append(startFields, "timing", timingField("list", listDur, "apply", applyDur))
	}
	jlog(log.LevelInfo, "prune", job, "pruning backups", startFields...)

	tDel := time.Now()
	if err := retention.DeleteBackups(st, toDelete); err != nil {
		delDur := time.Since(tDel)
		ef := []any{"status", "failed", "stage", "delete", "error", err.Error()}
		if timingsEnabled(job) {
			ef = append(ef, "timing", timingField("total", listDur+applyDur+delDur))
		}
		jlog(log.LevelError, "prune", job, "prune failed", ef...)
		return
	}
	if s.activity != nil {
		s.activity(job.Type, "prune", len(toDelete))
	}
	delDur := time.Since(tDel)

	completeFields := []any{"status", "complete", "deleted", len(toDelete), "policy", policyDesc}
	if timingsEnabled(job) {
		completeFields = append(completeFields, "timing",
			timingField("list", listDur, "apply", applyDur, "delete", delDur, "total", listDur+applyDur+delDur))
	}
	jlog(log.LevelInfo, "prune", job, "prune complete", completeFields...)
}

func archiveStorageCfg(job config.JobConfig) *config.StorageConfig {
	cfg := *job.Storage
	path := job.Archive.Path
	if path == "" {
		base := cfg.Path
		if base == "" {
			base = config.StoragePath() + "/" + job.Name
		}
		path = base + "/archive"
	}
	cfg.Path = path
	return &cfg
}

func (s *Scheduler) postBackup(job config.JobConfig) map[string]time.Duration {
	steps := map[string]time.Duration{}

	if job.Archive != nil && (job.Archive.Last > 0 || job.Archive.Within != "") {
		start := time.Now()
		s.applyArchive(job)
		steps["archive"] = time.Since(start)
	}

	if job.Retention != nil {
		start := time.Now()
		s.applyPrune(job, job.Storage, job.Retention)
		steps["prune"] = time.Since(start)
	}
	if job.Archive != nil && job.Archive.Retention != nil {
		start := time.Now()
		s.applyPrune(job, archiveStorageCfg(job), job.Archive.Retention)
		steps["prune"] += time.Since(start)
	}
	return steps
}

func retentionPolicy(ret *config.RetentionConfig) retention.RetentionPolicy {
	p := retention.RetentionPolicy{}
	if ret == nil {
		return p
	}
	if ret.Last != nil {
		p.Last = *ret.Last
	}
	if ret.Within != "" {
		if d, err := retention.ParseWithin(ret.Within); err == nil {
			p.Within = d
		}
	}
	if ret.Hourly != nil {
		p.Hourly = *ret.Hourly
	}
	if ret.Daily != nil {
		p.Daily = *ret.Daily
	}
	if ret.Weekly != nil {
		p.Weekly = *ret.Weekly
	}
	if ret.Monthly != nil {
		p.Monthly = *ret.Monthly
	}
	if ret.Yearly != nil {
		p.Yearly = *ret.Yearly
	}
	return p
}

func storageFromConfig(cfg *config.StorageConfig) (storage.Storage, error) {
	if cfg == nil {
		cfg = &config.StorageConfig{Backend: "filesystem", Path: config.StoragePath()}
	}
	return storage.New(storage.Backend(cfg.Backend), cfg.Options())
}

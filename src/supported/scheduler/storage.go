// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package schedulersupport

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
	"github.com/nfrastack/db-backup/internal/storage"
)

func init() {
	runner.UploadToProfiles = Upload
	runner.WriteProfileSidecars = WriteSidecars
	runner.LinkLatestProfiles = LinkLatest
}

type storageTarget struct {
	name    string
	storage *config.StorageConfig
	backend string
	path    string
	st      storage.Storage
	initErr error
}

type storageOutcome struct {
	name   string
	status string
	detail string
	bytes  int64
	err    error
}

type profileUploadResult struct {
	outcomes   []storageOutcome
	summary    string
	primaryErr error
}

func (r *profileUploadResult) record(name, status, detail string, n int64, err error) {
	r.outcomes = append(r.outcomes, storageOutcome{name: name, status: status, detail: detail, bytes: n, err: err})
}

func (r *profileUploadResult) summarize() {
	parts := make([]string, 0, len(r.outcomes))
	for _, o := range r.outcomes {
		part := o.name + "=" + o.status
		if o.detail != "" {
			part += ": " + o.detail
		}
		parts = append(parts, part)
	}
	r.summary = strings.Join(parts, ", ")
}

func outcomeDetail(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", " ")
}

func gate() error {
	if _, err := license.RequireValidLicense(); err != nil {
		return fmt.Errorf("multi-storage requires a Supporter license (not available in the Community edition): %s", err)
	}
	return nil
}

func resolveProfileTargets(job config.JobConfig) []storageTarget {
	targets := make([]storageTarget, 0, len(job.StorageProfiles))
	for i := range job.StorageProfiles {
		e := &job.StorageProfiles[i]
		tgt := storageTarget{name: e.StorageProfile, storage: e.Storage}
		if e.Storage != nil {
			tgt.backend = e.Storage.Backend
			tgt.path = e.Storage.Path
		} else {
			tgt.initErr = fmt.Errorf("storage profile %q is not resolved", e.StorageProfile)
		}
		targets = append(targets, tgt)
	}
	return targets
}

func openProfileTargets(job config.JobConfig, targets []storageTarget) {
	for i := range targets {
		tgt := &targets[i]
		if tgt.storage == nil {
			continue
		}
		switch strings.ToLower(tgt.backend) {
		case "azure", "gcs":
			if err := license.AllowStorage(); err != nil {
				g := license.Enabled(license.FeatureStorage)
				license.Notice(license.FeatureStorage, g)
				tgt.initErr = fmt.Errorf("%s storage is not available in the Community edition (%s)", tgt.backend, err)
				continue
			}
		}
		if tgt.storage.User != "" {
			currentUser := os.Getenv("USER")
			if currentUser == "" {
				currentUser = "root"
			}
			if currentUser != "root" && currentUser != tgt.storage.User {
				runner.JLog(log.LevelWarn, job, "running user may not own written files",
					"status", "warn", "user", currentUser, "storage_user", tgt.storage.User, "storage_profile", tgt.name)
			}
		}
		var err error
		tgt.st, err = storage.New(storage.Backend(tgt.backend), tgt.storage.Options())
		if err != nil {
			tgt.initErr = err
		}
	}
}

func Upload(ctx context.Context, job config.JobConfig, filename string, r io.Reader, abort bool) (int64, []bool, string, error) {
	if err := gate(); err != nil {
		return 0, nil, "", err
	}
	targets := resolveProfileTargets(job)
	openProfileTargets(job, targets)

	var res profileUploadResult
	spool, err := os.CreateTemp(storage.SpoolDir(), "dbb-spool-*")
	if err != nil {
		return 0, succeededProfiles(targets, res.outcomes), res.summary, err
	}
	spoolName := spool.Name()
	defer os.Remove(spoolName)

	n, copyErr := io.Copy(spool, r)
	if cerr := spool.Close(); cerr != nil && copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil {
		return 0, succeededProfiles(targets, res.outcomes), res.summary, copyErr
	}
	runner.JLog(log.LevelDebug, job, "spooled backup for multi-profile upload",
		"status", "debug", "step", "upload", "spool", spoolName, "bytes", n)

	stopped := false
	for i := range targets {
		tgt := &targets[i]
		if stopped {
			res.record(tgt.name, "skipped", "aborted after earlier failure", 0, nil)
			runner.JLog(log.LevelWarn, job, "storage skipped",
				"status", "skipped", "step", "upload:"+tgt.name, "storage_profile", tgt.name)
			continue
		}
		if tgt.initErr != nil {
			res.record(tgt.name, "failed", outcomeDetail(tgt.initErr), 0, tgt.initErr)
			runner.JLog(log.LevelWarn, job, "storage backend unavailable",
				"status", "warn", "step", "upload:"+tgt.name, "storage_profile", tgt.name, "error", tgt.initErr.Error())
		} else {
			f, oerr := os.Open(spoolName)
			if oerr != nil {
				res.record(tgt.name, "failed", outcomeDetail(oerr), 0, oerr)
				runner.JLog(log.LevelWarn, job, "spool unreadable",
					"status", "warn", "step", "upload:"+tgt.name, "storage_profile", tgt.name, "error", oerr.Error())
			} else {
				runner.JLog(log.LevelDebug, job, "writing backup to storage",
					"status", "debug", "step", "upload:"+tgt.name, "target", tgt.path+"/"+filename,
					"backend", tgt.backend)
				nb, uerr := tgt.st.Upload(ctx, filename, f)
				f.Close()
				if nb == 0 {
					nb = n
				}
				if uerr != nil {
					res.record(tgt.name, "failed", outcomeDetail(uerr), nb, uerr)
					runner.JLog(log.LevelWarn, job, "storage upload failed",
						"status", "warn", "step", "upload:"+tgt.name, "storage_profile", tgt.name, "error", uerr.Error())
				} else {
					res.record(tgt.name, "ok", "", nb, nil)
					runner.JLog(log.LevelInfo, job, "backup uploaded",
						"status", "complete", "step", "upload:"+tgt.name, "storage_profile", tgt.name,
						"file", tgt.path+"/"+filename, "bytes", nb)
				}
			}
		}
		last := res.outcomes[len(res.outcomes)-1]
		if last.status == "failed" {
			if i == 0 {
				res.primaryErr = last.err
			}
			if abort {
				stopped = true
			}
		}
	}
	res.summarize()
	runner.JLog(log.LevelInfo, job, "multi-profile upload complete",
		"status", "complete", "step", "upload", "profiles", res.summary)
	if res.primaryErr != nil {
		return n, succeededProfiles(targets, res.outcomes), res.summary, res.primaryErr
	}
	return n, succeededProfiles(targets, res.outcomes), res.summary, nil
}

func succeededProfiles(targets []storageTarget, outcomes []storageOutcome) []bool {
	out := make([]bool, len(targets))
	for i := range outcomes {
		if i < len(out) && outcomes[i].status == "ok" {
			out[i] = true
		}
	}
	return out
}

func WriteSidecars(job config.JobConfig, filename string, sc *retention.Sidecar, succeeded []bool, abort bool) (string, error) {
	if err := gate(); err != nil {
		return "", err
	}
	targets := resolveProfileTargets(job)
	openProfileTargets(job, targets)

	var res profileUploadResult
	stopped := false
	for i := range targets {
		tgt := &targets[i]
		if i >= len(succeeded) || !succeeded[i] {
			continue
		}
		if stopped {
			res.record(tgt.name, "skipped", "aborted after earlier failure", 0, nil)
			runner.JLog(log.LevelWarn, job, "sidecar skipped",
				"status", "skipped", "step", "sidecar:"+tgt.name, "storage_profile", tgt.name)
			continue
		}
		if tgt.initErr != nil {
			continue
		}
		if err := retention.WriteSidecar(tgt.st, filename, sc); err != nil {
			res.record(tgt.name, "failed", outcomeDetail(err), 0, err)
			runner.JLog(log.LevelWarn, job, "sidecar write failed",
				"status", "warn", "step", "sidecar:"+tgt.name, "storage_profile", tgt.name, "error", err.Error())
			if i == 0 {
				res.primaryErr = err
			}
			if abort {
				stopped = true
			}
			continue
		}
		res.record(tgt.name, "ok", "", 0, nil)
		runner.JLog(log.LevelDebug, job, "sidecar written",
			"status", "debug", "step", "sidecar:"+tgt.name, "file", filename+".json")
	}
	res.summarize()
	if res.primaryErr != nil {
		return res.summary, res.primaryErr
	}
	return res.summary, nil
}

func LinkLatest(job config.JobConfig, dbName, filename string, succeeded []bool) {
	if job.Backup == nil || job.Backup.CreateLatest == nil || !*job.Backup.CreateLatest {
		return
	}
	targets := resolveProfileTargets(job)
	for i := range targets {
		if i < len(succeeded) && !succeeded[i] {
			continue
		}
		tgt := &targets[i]
		if tgt.initErr != nil {
			continue
		}
		runner.CreateLatestSymlinkFor(job, dbName, tgt.path, filename, tgt.backend)
	}
}

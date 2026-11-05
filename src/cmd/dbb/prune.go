// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
	"github.com/nfrastack/db-backup/internal/storage"
)

func cmdPrune(args []string) int {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	storagePath := fs.String("storage-path", config.StoragePath(), "Storage path/prefix (filesystem)")
	storageProfile := fs.String("storage-profile", "", "Storage profile (resolved from -c <config>)")
	last := fs.Int("last", 0, "Keep the N most recent backups")
	within := fs.String("within", "", "Keep everything newer than the window (10m, 24h, 7d - bare = hours)")
	hourly := fs.Int("hourly", 0, "Backups to keep per hour")
	daily := fs.Int("daily", 7, "Daily backups to keep")
	weekly := fs.Int("weekly", 4, "Weekly backups to keep")
	monthly := fs.Int("monthly", 6, "Monthly backups to keep")
	yearly := fs.Int("yearly", 2, "Yearly backups to keep")
	dryRun := fs.Bool("dry-run", false, "Print what would be deleted without doing it")
	fs.Parse(args)

	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	cfgGFS := false
	var jobStorage *config.StorageConfig

	if len(globalConfigPaths) > 0 {
		cfg, err := config.LoadConfig(globalConfigPaths...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: config: %v\n", err)
			return 1
		}
		if cfg.Defaults != nil && cfg.Defaults.Retention != nil {
			if cfg.Defaults.Retention.Last != nil && *last == 0 {
				*last = *cfg.Defaults.Retention.Last
			}
			if cfg.Defaults.Retention.Within != "" && *within == "" {
				*within = cfg.Defaults.Retention.Within
			}
			if cfg.Defaults.Retention.Hourly != nil {
				*hourly = *cfg.Defaults.Retention.Hourly
				cfgGFS = true
			}
			if cfg.Defaults.Retention.Daily != nil {
				*daily = *cfg.Defaults.Retention.Daily
				cfgGFS = true
			}
			if cfg.Defaults.Retention.Weekly != nil {
				*weekly = *cfg.Defaults.Retention.Weekly
				cfgGFS = true
			}
			if cfg.Defaults.Retention.Monthly != nil {
				*monthly = *cfg.Defaults.Retention.Monthly
				cfgGFS = true
			}
			if cfg.Defaults.Retention.Yearly != nil {
				*yearly = *cfg.Defaults.Retention.Yearly
				cfgGFS = true
			}
		}
		if len(cfg.Jobs) > 0 {
			jobNames := fs.Args()
			if len(jobNames) > 0 {
				for _, jn := range jobNames {
					for _, job := range cfg.Jobs {
						if job.Name == jn {
							jobStorage = job.Storage
							break
						}
					}
				}
			} else {
				jobStorage = cfg.Jobs[0].Storage
			}
		}
	}

	storageCfg := jobStorage
	if *storageProfile != "" {
		var err error
		storageCfg, err = config.ResolveStorageArg(globalConfigPaths, *storageProfile, *storagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
	}
	if storageCfg == nil {
		var err error
		storageCfg, err = config.ResolveStorageArg(globalConfigPaths, "", *storagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
	}
	st, err := storage.New(storage.Backend(storageCfg.Backend), runner.StorageOpts(storageCfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", err)
		return 1
	}

	backups, err := retention.ListBackupsWithSidecars(st, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: list: %v\n", err)
		return 1
	}

	if len(backups) == 0 {
		fmt.Fprintln(os.Stderr, "No backups found")
		return 0
	}

	policy := retention.RetentionPolicy{
		Last: *last,
	}
	if explicit["hourly"] || cfgGFS {
		policy.Hourly = *hourly
	}
	if explicit["daily"] || cfgGFS {
		policy.Daily = *daily
	}
	if explicit["weekly"] || cfgGFS {
		policy.Weekly = *weekly
	}
	if explicit["monthly"] || cfgGFS {
		policy.Monthly = *monthly
	}
	if explicit["yearly"] || cfgGFS {
		policy.Yearly = *yearly
	}
	if *within != "" {
		d, err := retention.ParseWithin(*within)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		policy.Within = d
	}

	if !policy.Valid() {
		fmt.Fprintln(os.Stderr, "ERROR: no retention rules configured (set --last, --within, or GFS tiers) - nothing would ever be kept")
		return 1
	}

	if err := retention.CheckPrune(policy); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}

	toDelete := retention.ApplyRetention(backups, policy)

	if len(toDelete) == 0 {
		fmt.Fprintln(os.Stderr, "Nothing to delete")
		return 0
	}

	fmt.Fprintf(os.Stderr, "retention %s would delete %d backup(s):\n", policy.Describe(), len(toDelete))
	for _, f := range toDelete {
		fmt.Fprintf(os.Stderr, "  %s\n", f)
	}

	if *dryRun {
		return 0
	}

	if err := retention.DeleteBackups(st, toDelete); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: delete: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Deleted %d backup(s)\n", len(toDelete))
	return 0
}

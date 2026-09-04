// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
	"github.com/nfrastack/db-backup/internal/storage"
)

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	storagePath := fs.String("storage-path", config.StoragePath(), "Storage path/prefix (filesystem)")
	storageProfile := fs.String("storage-profile", "", "Storage profile (resolved from -c <config>)")
	prefix := fs.String("prefix", "", "Filter by prefix")
	fs.Parse(args)

	if len(globalConfigPaths) > 0 && *storageProfile == "" {
		explicit := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "storage-path" {
				explicit = true
			}
		})
		if !explicit && *storagePath == config.StoragePath() {
			if cfg, err := config.LoadConfig(globalConfigPaths...); err == nil {
				if cfg.Defaults != nil && cfg.Defaults.Storage != nil && cfg.Defaults.Storage.Path != "" {
					*storagePath = cfg.Defaults.Storage.Path
				} else if len(cfg.Jobs) > 0 && cfg.Jobs[0].Storage != nil && cfg.Jobs[0].Storage.Path != "" {
					*storagePath = cfg.Jobs[0].Storage.Path
				} else if path := cfg.StorageProfiles["fs"]; path.Path != "" {
					*storagePath = path.Path
				}
			}
		}
	}

	storageCfg, err := config.ResolveStorageArg(globalConfigPaths, *storageProfile, *storagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	st, err := storage.New(storage.Backend(storageCfg.Backend), runner.StorageOpts(storageCfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", err)
		return 1
	}

	entries, err := st.List(context.Background(), *prefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: list: %v\n", err)
		return 1
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No backups found")
		return 0
	}

	var backups []database.BackupInfo
	for _, e := range entries {
		if !database.IsBackupFile(e.Path) {
			continue
		}
		bi, err := database.ParseBackupFilename(e.Path)
		if err != nil {
			continue
		}
		bi.Size = e.Size
		if sc, err := retention.ReadSidecar(st, e.Path); err == nil && sc != nil {
			if sc.DB != "" {
				bi.DBName = sc.DB
			}
			if sc.Host != "" {
				bi.Host = sc.Host
			}
			if sc.Type != "" {
				bi.Type = sc.Type
			}
			if sc.Strategy != "" {
				bi.Strategy = sc.Strategy
			}
		}
		backups = append(backups, *bi)
	}

	if len(backups) == 0 {
		fmt.Fprintln(os.Stderr, "No valid backup files found (try --prefix or check storage path)")
		return 0
	}

	fmt.Fprintf(os.Stdout, "%-8s %-16s %-20s %-10s %-22s %10s  %s\n",
		"TYPE", "DB", "HOST", "STRATEGY", "TIMESTAMP", "SIZE", "COMPRESS")
	for _, b := range backups {
		compress := b.Compress
		if b.Encryption != "" {
			compress += "+" + b.Encryption
		}
		fmt.Fprintf(os.Stdout, "%-8s %-16s %-20s %-10s %-22s %10s  %s\n",
			b.Type, b.DBName, b.Host, b.Strategy,
			b.Timestamp.Format("2006-01-02 15:04:05"),
			b.DisplaySize(), compress)
	}
	return 0
}

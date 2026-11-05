// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/storage"
)

func archiveStorageConfig(side, profile, path string) (*config.StorageConfig, error) {
	if profile != "" {
		if len(globalConfigPaths) == 0 {
			return nil, fmt.Errorf("--%s-storage-profile requires -c <config>", side)
		}
		return config.LoadStorageProfile(globalConfigPaths, profile)
	}
	return &config.StorageConfig{Backend: "filesystem", Path: path}, nil
}

func cmdArchive(args []string) int {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	last := fs.Int("last", 0, "Number of latest backups to keep in hot storage")
	within := fs.String("within", "", "Keep backups newer than this window (10m, 24h, 7d) in hot storage, archive older ones")
	srcProfile := fs.String("src-storage-profile", "", "Source storage profile (resolved from -c <config>)")
	srcPath := fs.String("src-storage-path", config.StoragePath(), "Source storage path (filesystem)")
	dstProfile := fs.String("dst-storage-profile", "", "Destination storage profile (resolved from -c <config>)")
	dstPath := fs.String("dst-storage-path", "", "Destination storage path (filesystem)")
	dryRun := fs.Bool("dry-run", false, "Print what would be archived without doing it")
	fs.Parse(args)

	srcCfg, err := archiveStorageConfig("src", *srcProfile, *srcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	dstCfg, err := archiveStorageConfig("dst", *dstProfile, *dstPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}

	if *dstProfile == "" && *dstPath == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --dst-storage-path is required (or use --dst-storage-profile)\n")
		return 1
	}

	srcSt, err := storage.New(storage.Backend(srcCfg.Backend), srcCfg.Options())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: source storage: %v\n", err)
		return 1
	}

	dstSt, err := storage.New(storage.Backend(dstCfg.Backend), dstCfg.Options())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: destination storage: %v\n", err)
		return 1
	}

	var withinDur time.Duration
	if *within != "" {
		withinDur, err = retention.ParseWithin(*within)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
	}

	if *last == 0 && withinDur == 0 {
		fmt.Fprintln(os.Stderr, "ERROR: set --last N or --within WINDOW to decide what stays in hot storage")
		return 1
	}

	acfg := &retention.ArchiveConfig{
		Last:   *last,
		Within: withinDur,
		Src:    srcSt,
		Dst:    dstSt,
	}

	if *dryRun {
		entries, _ := srcSt.List(context.Background(), "")
		type backupEntry struct {
			path    string
			modTime int64
		}
		var backups []backupEntry
		for _, e := range entries {
			if database.IsBackupFile(e.Path) {
				backups = append(backups, backupEntry{path: e.Path, modTime: e.ModTime})
			}
		}
		// Sort newest-first so backups[keep:] skips the correct entries.
		sort.Slice(backups, func(i, j int) bool {
			return backups[i].modTime > backups[j].modTime
		})
		keep := *last
		if withinDur > 0 {
			cutoff := time.Now().Add(-withinDur).UnixNano()
			keep = 0
			for _, b := range backups {
				if b.modTime >= cutoff {
					keep++
				}
			}
		}
		if len(backups) <= keep {
			fmt.Fprintf(os.Stderr, "Nothing to archive (%d backups, keep=%d)\n", len(backups), keep)
			return 0
		}
		fmt.Fprintf(os.Stderr, "Would archive %d backup(s) to %s/%s:\n",
			len(backups)-keep, dstCfg.Backend, dstCfg.Path)
		for _, b := range backups[keep:] {
			fmt.Fprintf(os.Stderr, "  %s\n", b.path)
		}
		return 0
	}

	moved, _, err := retention.RunArchive(acfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: archive: %v\n", err)
		return 1
	}
	if moved == 0 {
		fmt.Fprintln(os.Stderr, "Nothing to archive")
	} else {
		fmt.Fprintf(os.Stderr, "Archived %d backup(s) to %s/%s\n", moved, dstCfg.Backend, dstCfg.Path)
	}
	return 0
}

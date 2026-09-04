// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package backend

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/storage"
)

func init() {
	retention.ArchiveRunner = runArchive
}

func moveFile(src, dst storage.Storage, path string) error {
	if path == "" {
		return nil
	}
	rc, _, err := src.Download(context.Background(), path)
	if err != nil {
		return nil
	}
	defer rc.Close()

	if _, err := dst.Upload(context.Background(), path, rc); err != nil {
		return fmt.Errorf("upload to archive: %w", err)
	}

	if err := src.Delete(context.Background(), path); err != nil {
		return fmt.Errorf("delete from hot: %w", err)
	}

	return nil
}
func runArchive(cfg *retention.ArchiveConfig) (int, []string, error) {
	entries, err := cfg.Src.List(context.Background(), "")
	if err != nil {
		return 0, nil, fmt.Errorf("list: %w", err)
	}

	type backupFile struct {
		path    string
		modTime int64
	}
	var backups []backupFile
	for _, e := range entries {
		if strings.HasSuffix(e.Path, ".json") {
			continue
		}
		if !database.IsBackupFile(e.Path) {
			continue
		}
		backups = append(backups, backupFile{path: e.Path, modTime: e.ModTime})
	}

	if len(backups) == 0 {
		return 0, nil, nil
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime > backups[j].modTime
	})

	var keep int
	switch {
	case cfg.Within > 0:
		cutoff := time.Now().Add(-cfg.Within).UnixNano()
		for keep < len(backups) && backups[keep].modTime >= cutoff {
			keep++
		}
	case cfg.Last > 0:
		keep = cfg.Last
	}

	if keep >= len(backups) {
		return 0, nil, nil
	}

	toArchive := backups[keep:]
	candidates := make([]string, 0, len(toArchive))
	var moved int

	for _, b := range toArchive {
		log.Debug("archive", "moved file", "file", b.path, "status", "debug")
		candidates = append(candidates, b.path)
		if err := moveFile(cfg.Src, cfg.Dst, b.path); err != nil {
			return moved, candidates, fmt.Errorf("move %s: %w", b.path, err)
		}
		moveFile(cfg.Src, cfg.Dst, retention.SidecarName(b.path))
		for _, ext := range []string{".md5", ".sha1"} {
			moveFile(cfg.Src, cfg.Dst, b.path+ext)
		}
		moved++
	}

	return moved, candidates, nil
}

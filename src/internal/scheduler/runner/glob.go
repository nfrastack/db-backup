// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/storage"
)

func IsGlobPattern(s string) bool {
	for _, r := range s {
		if r == '*' || r == '?' || r == '[' {
			return true
		}
	}
	return false
}

func ResolveGlobFile(st storage.Storage, pattern string) (string, error) {
	entries, err := st.List(context.Background(), "")
	if err != nil {
		return "", fmt.Errorf("list: %w", err)
	}

	var best storage.Entry
	var bestTS time.Time
	found := false
	for _, e := range entries {
		if !database.IsBackupFile(e.Path) {
			continue
		}
		ok, err := path.Match(pattern, e.Path)
		if err != nil {
			return "", fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		if !ok {
			continue
		}
		bi, _ := database.ParseBackupFilename(e.Path)
		if !found {
			best, bestTS, found = e, bi.Timestamp, true
			continue
		}
		if e.ModTime > best.ModTime || (e.ModTime == best.ModTime && bi.Timestamp.After(bestTS)) {
			best, bestTS = e, bi.Timestamp
		}
	}
	if !found {
		return "", fmt.Errorf("no backups match %q", pattern)
	}
	return best.Path, nil
}

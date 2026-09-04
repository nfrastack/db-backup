// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/storage"
)

type backupChain struct {
	Depth    int
	LastFull int64
}

func backupPrefix(dbType, dbName, host string) string {
	job := config.JobConfig{Type: dbType}
	return fmt.Sprintf("%s-%s-%s-", dbType, dbToken(job, dbName), hostSanitizer.Replace(host))
}

func chainInfo(st storage.Storage, dbType, dbName, host string) backupChain {
	entries, err := st.List(context.Background(), "")
	if err != nil {
		return backupChain{}
	}
	prefix := backupPrefix(dbType, dbName, host)
	var all []storage.Entry
	for _, e := range entries {
		if !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		if strings.HasSuffix(e.Path, ".json") {
			continue
		}
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].ModTime > all[j].ModTime
	})

	chain := backupChain{}
	for _, e := range all {
		sc, err := retention.ReadSidecar(st, e.Path)
		if err != nil {
			continue
		}
		if sc.Strategy == "full" {
			chain.LastFull = e.ModTime
			break
		}
		chain.Depth++
	}
	return chain
}
func containsGlobals(include []string) bool {
	for _, d := range include {
		if strings.Contains(d, "__globals__") {
			return true
		}
	}
	return false
}

func findEntry(entries []storage.Entry, path string) (storage.Entry, bool) {
	for _, e := range entries {
		if e.Path == path {
			return e, true
		}
	}
	return storage.Entry{}, false
}

func findLastChainParent(st storage.Storage, dbType, dbName, host string) string {
	entries, err := st.List(context.Background(), "")
	if err != nil {
		return ""
	}
	var latest string
	var latestTS int64
	prefix := backupPrefix(dbType, dbName, host)
	for _, e := range entries {
		if strings.HasSuffix(e.Path, ".json") || !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		if e.ModTime >= latestTS {
			latest = e.Path
			latestTS = e.ModTime
		}
	}
	return latest
}

func findLastFullBackup(st storage.Storage, dbType, dbName, host string) string {
	entries, err := st.List(context.Background(), "")
	if err != nil {
		return ""
	}
	var latest string
	var latestTS int64
	prefix := backupPrefix(dbType, dbName, host) + "full-"
	for _, e := range entries {
		if strings.HasSuffix(e.Path, ".json") {
			continue
		}
		if strings.HasPrefix(e.Path, prefix) && e.ModTime >= latestTS {
			latest = e.Path
			latestTS = e.ModTime
		}
	}
	return latest
}

func findLastPosition(st storage.Storage, dbType, dbName, host, strategy string) string {
	entries, err := st.List(context.Background(), "")
	if err != nil {
		return ""
	}

	searchPrefix := backupPrefix(dbType, dbName, host)
	var candidates []storage.Entry
	for _, e := range entries {
		if !strings.HasPrefix(e.Path, searchPrefix) {
			continue
		}
		if strings.HasSuffix(e.Path, ".json") {
			continue
		}
		if strategy == "differential" && !retention.StrategyMatches(e.Path, "full") {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ModTime == candidates[j].ModTime {
			return candidates[i].Path > candidates[j].Path
		}
		return candidates[i].ModTime > candidates[j].ModTime
	})
	for _, e := range candidates {
		sc, err := retention.ReadSidecar(st, e.Path)
		if err != nil {
			log.Debug("backup", "no sidecar for position lookup", "file", e.Path, "error", err.Error())
			continue
		}
		if sc.Position != "" {
			log.Debug("backup", "found position", "file", e.Path, "position", sc.Position)
			return sc.Position
		}
	}
	return ""
}

func incrementalEngineRegistered(dbType string) bool {
	return database.LookupIncremental(dbType) != nil
}

func positionAnchored(dbType string) bool {
	switch strings.ToLower(dbType) {
	case "mongo", "mongodb", "mysql", "mariadb":
		return true
	}
	return false
}

func shouldResetChain(cfg *config.BackupConfig, chain backupChain) string {
	if chain.LastFull == 0 {
		return "no previous full backup (chain needs a base)"
	}
	if cfg == nil {
		return ""
	}
	if cfg.FullEvery > 0 && chain.Depth >= cfg.FullEvery {
		return fmt.Sprintf("chain depth %d reached full_every=%d", chain.Depth, cfg.FullEvery)
	}
	if cfg.FullAfter != "" {
		if d, err := time.ParseDuration(cfg.FullAfter); err == nil && d > 0 {
			if time.Since(time.Unix(chain.LastFull, 0)) >= d {
				return "last full backup older than full_after=" + cfg.FullAfter
			}
		}
	}
	return ""
}

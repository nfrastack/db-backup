// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/storage"
)

type BackupEntry struct {
	FileName  string
	Timestamp time.Time
	Strategy  string
	Base      string
	Size      int64
}

type RetentionPolicy struct {
	Last    int
	Within  time.Duration
	Hourly  int
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

var SupporterParseWithin = func(s string) (time.Duration, error) {
	return 0, fmt.Errorf("keep_within %q requires Supporter license (community supports minutes only, eg \"1440m\")", s)
}

func ApplyRetention(backups []BackupEntry, policy RetentionPolicy) []string {
	return ApplyRetentionAt(backups, policy, time.Time{})
}

func ApplyRetentionAt(backups []BackupEntry, policy RetentionPolicy, now time.Time) []string {
	if now.IsZero() {
		now = time.Now()
	}
	if len(backups) == 0 {
		return nil
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Timestamp.After(backups[j].Timestamp)
	})

	needed := chainDependencies(backups)

	kept := make(map[string]bool)
	for _, b := range backups {
		if needed[b.FileName] {
			kept[b.FileName] = true
		}
	}

	if policy.Within > 0 {
		cutoff := now.Add(-policy.Within)
		if backups[0].Timestamp.IsZero() {
			log.Warn("retention", "within cutoff using now() because newest timestamp is zero (missing sidecar/timestamp parse failed)", "within", policy.Within, "fallback_cutoff", cutoff)
		} else if now.Sub(backups[0].Timestamp) > 2*policy.Within {
			log.Debug("retention", "newest backup is stale, using now() for within window", "newest", backups[0].Timestamp, "within", policy.Within, "cutoff", cutoff)
		}
		for _, b := range backups {
			if b.Timestamp.IsZero() {
				continue
			}
			if !b.Timestamp.Before(cutoff) {
				kept[b.FileName] = true
			}
		}
	}

	if policy.Last > 0 {
		count := 0
		for _, b := range backups {
			if count >= policy.Last {
				break
			}
			kept[b.FileName] = true
			count++
		}
	}

	if policy.HasGFS() {
		if err := CheckPrune(policy); err != nil {
			log.Warn("retention", "GFS tiers not available in Community edition, falling back to last/within only",
				"error", err, "policy", policy.Describe())
			policy.StripTiers()
		}
	}

	TierHook(backups, policy, kept)

	var toDelete []string
	for _, b := range backups {
		if !kept[b.FileName] {
			toDelete = append(toDelete, b.FileName)
		}
	}
	return toDelete
}

func DeleteBackups(st storage.Storage, filenames []string) error {
	return DeleteBackupsWithContext(context.Background(), st, filenames)
}

func DeleteBackupsWithContext(ctx context.Context, st storage.Storage, filenames []string) error {
	var errs []string
	for _, f := range filenames {
		log.Debug("prune", "deleted file", "file", f, "status", "debug")
		if err := st.Delete(ctx, f); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", f, err))
		}
		if err := st.Delete(ctx, SidecarName(f)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("%s: %v", SidecarName(f), err))
		}
		for _, ext := range []string{".md5", ".sha1"} {
			if err := st.Delete(ctx, f+ext); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("%s: %v", f+ext, err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("delete errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
func (p RetentionPolicy) Describe() string {
	var parts []string
	if p.Last > 0 {
		parts = append(parts, fmt.Sprintf("last=%d", p.Last))
	}
	if p.Within > 0 {
		parts = append(parts, fmt.Sprintf("within=%s", formatDuration(p.Within)))
	}
	if p.Hourly > 0 {
		parts = append(parts, fmt.Sprintf("hourly=%d", p.Hourly))
	}
	if p.Daily > 0 {
		parts = append(parts, fmt.Sprintf("daily=%d", p.Daily))
	}
	if p.Weekly > 0 {
		parts = append(parts, fmt.Sprintf("weekly=%d", p.Weekly))
	}
	if p.Monthly > 0 {
		parts = append(parts, fmt.Sprintf("monthly=%d", p.Monthly))
	}
	if p.Yearly > 0 {
		parts = append(parts, fmt.Sprintf("yearly=%d", p.Yearly))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func ListBackupsWithSidecars(st storage.Storage, prefix string) ([]BackupEntry, error) {
	entries, err := st.List(context.Background(), prefix)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	sidecars := make(map[string]*Sidecar)
	for _, e := range entries {
		if strings.HasSuffix(e.Path, ".json") {
			name := strings.TrimSuffix(e.Path, ".json")
			sc, err := ReadSidecar(st, name)
			if err == nil {
				sidecars[name] = sc
			}
		}
	}

	var backups []BackupEntry
	for _, e := range entries {
		if strings.HasSuffix(e.Path, ".json") {
			continue
		}
		if !database.IsBackupFile(e.Path) {
			continue
		}

		sc, hasSC := sidecars[e.Path]
		var ts time.Time
		strategy := "full"
		base := ""
		if hasSC {
			t, err := time.Parse(time.RFC3339, sc.Timestamp)
			if err == nil {
				ts = t
			}
			strategy = sc.Strategy
			base = sc.Base
		}
		if ts.IsZero() {
			t, ok := ParseTimestamp(e.Path)
			if ok {
				ts = t
			}
		}
		if strategy == "full" {
			strategy = StrategyFromFilename(e.Path)
		}

		backups = append(backups, BackupEntry{
			FileName:  e.Path,
			Timestamp: ts,
			Strategy:  strategy,
			Base:      base,
			Size:      e.Size,
		})
	}

	return backups, nil
}

func ParseWithin(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, nil
	}

	if strings.HasSuffix(trimmed, "m") {
		num := strings.TrimSuffix(trimmed, "m")
		n, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid keep_within %q: expected Ns, Nm, Nh, or Nd (eg 90s, 10m, 24h, 7d)", s)
		}
		return time.Duration(n) * time.Minute, nil
	}
	return SupporterParseWithin(trimmed)
}
func (p RetentionPolicy) Valid() bool {
	return p.Last > 0 || p.Within > 0 ||
		p.Hourly > 0 || p.Daily > 0 || p.Weekly > 0 || p.Monthly > 0 || p.Yearly > 0
}

func chainDependencies(backups []BackupEntry) map[string]bool {
	needed := make(map[string]bool)
	for _, b := range backups {
		if b.Strategy == "full" {
			continue
		}
		base := b.Base
		for base != "" {
			if needed[base] {
				break
			}
			needed[base] = true
			found := false
			for _, b2 := range backups {
				if b2.FileName == base {
					base = b2.Base
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
	}
	return needed
}

func formatDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

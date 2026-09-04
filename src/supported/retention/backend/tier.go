// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package backend

import (
	"fmt"
	"time"

	"github.com/nfrastack/db-backup/internal/retention"
)

func applyTiers(backups []retention.BackupEntry, policy retention.RetentionPolicy, kept map[string]bool) {
	applyTier := func(n int, key func(time.Time) string) {
		if n <= 0 {
			return
		}
		seen := make(map[string]bool)
		for _, b := range backups {
			if kept[b.FileName] {
				continue
			}
			k := key(b.Timestamp)
			if len(seen) < n && !seen[k] {
				kept[b.FileName] = true
				seen[k] = true
			}
		}
	}

	applyTier(policy.Hourly, func(t time.Time) string { return t.Truncate(time.Hour).Format("2006-01-02T15") })
	applyTier(policy.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	applyTier(policy.Weekly, func(t time.Time) string {
		yr, wk := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", yr, wk)
	})
	applyTier(policy.Monthly, func(t time.Time) string { return t.Format("2006-01") })
	applyTier(policy.Yearly, func(t time.Time) string { return fmt.Sprintf("%d", t.Year()) })
}
func init() {
	retention.SetTierHook(applyTiers)
}

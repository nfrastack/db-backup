// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
)

const (
	defaultNoticeMaxShows   = 3
	defaultNoticeEveryHours = 24
)

const criticalVersionEveryHours = 24

func dueNotice(shown config.NoticeShow, maxShows, everyHours, everyChecks, totalChecks int, now time.Time) bool {
	if maxShows > 0 && shown.Count >= maxShows {
		return false
	}
	if shown.Count == 0 {
		return true
	}
	if everyHours > 0 && shown.LastShown != "" {
		if last, err := time.Parse(time.RFC3339, shown.LastShown); err == nil {
			if now.Sub(last) < time.Duration(everyHours)*time.Hour {
				return false
			}
		}
	}
	if everyChecks > 0 && totalChecks-shown.LastCheck < everyChecks {
		return false
	}
	return true
}

func effectiveNotices(resp *VersionResponse) []VersionNotice {
	if resp == nil {
		return nil
	}
	if len(resp.Notices) > 0 {
		return resp.Notices
	}
	if resp.Message == "" {
		return nil
	}
	return []VersionNotice{{
		ID:       "message:" + resp.Latest,
		Text:     resp.Message,
		Severity: "info",
		MaxShows: 1,
	}}
}

func logNotice(n VersionNotice) {
	msg := n.Text
	switch n.Severity {
	case "warn":
		log.Warn("version-check", msg, "notice_id", n.ID, "severity", n.Severity)
	case "critical":
		log.Error("version-check", msg, "notice_id", n.ID, "severity", n.Severity)
	default:
		log.Info("version-check", msg, "notice_id", n.ID, "severity", n.Severity)
	}
}

func noticeCaps(n VersionNotice) (maxShows, everyHours, everyChecks int) {
	maxShows, everyHours, everyChecks = n.MaxShows, n.EveryHours, n.EveryChecks
	if maxShows == 0 && everyHours == 0 && everyChecks == 0 {
		return defaultNoticeMaxShows, defaultNoticeEveryHours, 0
	}
	return maxShows, everyHours, everyChecks
}

func (m *Manager) noticeDue(id string, maxShows, everyHours, everyChecks, totalChecks int, now time.Time) bool {
	ledger := m.noticeLedger()
	if ledger == nil {
		return true
	}
	return dueNotice(ledger[id], maxShows, everyHours, everyChecks, totalChecks, now)
}

func (m *Manager) noticeLedger() map[string]config.NoticeShow {
	if m == nil || m.vstate == nil {
		return nil
	}
	if m.vstate.Notices == nil {
		m.vstate.Notices = make(map[string]config.NoticeShow)
	}
	return m.vstate.Notices
}

func (m *Manager) recordNoticeShow(id string, totalChecks int, now time.Time) {
	ledger := m.noticeLedger()
	if ledger == nil {
		return
	}
	shown := ledger[id]
	shown.Count++
	shown.LastShown = now.UTC().Format(time.RFC3339)
	shown.LastCheck = totalChecks
	ledger[id] = shown
}

func (m *Manager) selectNotices(now time.Time, totalChecks int, notices []VersionNotice) []VersionNotice {
	var due []VersionNotice
	for _, n := range notices {
		if n.Text == "" {
			continue
		}
		if n.Tracked != nil && !*n.Tracked {
			due = append(due, n)
			continue
		}
		id := n.ID
		if id == "" {
			id = "notice:" + n.Text
		}
		maxShows, everyHours, everyChecks := noticeCaps(n)
		if !m.noticeDue(id, maxShows, everyHours, everyChecks, totalChecks, now) {
			continue
		}
		m.recordNoticeShow(id, totalChecks, now)
		due = append(due, n)
	}
	return due
}

func versionNoticeID(latest string) string { return "version:" + latest }

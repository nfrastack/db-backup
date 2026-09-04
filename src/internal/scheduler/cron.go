// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
)

func cronMatch(expr string, t time.Time) bool {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return false
	}
	return fieldMatch(parts[0], t.Minute(), 0, 59) &&
		fieldMatch(parts[1], t.Hour(), 0, 23) &&
		fieldMatch(parts[2], t.Day(), 1, 31) &&
		fieldMatch(parts[3], int(t.Month()), 1, 12) &&
		fieldMatch(parts[4], int(t.Weekday()), 0, 6)
}

func fieldMatch(field string, val, min, max int) bool {
	if field == "*" {
		return true
	}
	if strings.Contains(field, ",") {
		for _, p := range strings.Split(field, ",") {
			if fieldMatch(p, val, min, max) {
				return true
			}
		}
		return false
	}
	if strings.Contains(field, "/") {
		parts := strings.SplitN(field, "/", 2)
		var step int
		fmt.Sscanf(parts[1], "%d", &step)
		if step <= 0 {
			return false
		}
		if parts[0] == "*" {
			return (val-min)%step == 0
		}
		var start int
		fmt.Sscanf(parts[0], "%d", &start)
		return val >= start && (val-start)%step == 0
	}
	if strings.Contains(field, "-") {
		var a, b int
		fmt.Sscanf(field, "%d-%d", &a, &b)
		return val >= a && val <= b
	}
	var n int
	fmt.Sscanf(field, "%d", &n)
	return val == n
}

func nextCron(expr string) time.Duration {
	now := time.Now()
	next := now.Add(time.Minute).Truncate(time.Minute)
	for i := 0; i < 525600; i++ {
		if cronMatch(expr, next) {
			return next.Sub(now)
		}
		next = next.Add(time.Minute)
	}
	return 0
}

func nextJobDuration(s *config.Schedule) time.Duration {
	if s == nil {
		return 0
	}
	if s.Cron != "" {
		return nextCron(s.Cron)
	}
	if s.Interval > 0 {
		return time.Duration(s.Interval) * time.Minute
	}
	if s.Begin != "" {
		return parseBegin(s.Begin)
	}
	if !s.Time.Empty() {
		if next, ok := s.Time.Next(time.Now()); ok {
			return next.Sub(time.Now())
		}
		return 0
	}
	return 0
}

func parseBegin(begin string) time.Duration {
	if strings.HasPrefix(begin, "+") {
		var m int
		fmt.Sscanf(begin, "+%d", &m)
		if m > 0 {
			return time.Duration(m) * time.Minute
		}
		return 0
	}
	if len(begin) == 4 {
		var h, m int
		fmt.Sscanf(begin, "%2d%2d", &h, &m)
		now := time.Now()
		target := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
		if target.Before(now) {
			target = target.Add(24 * time.Hour)
		}
		return target.Sub(now)
	}
	return 0
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func roundDur(d time.Duration) string {
	d = d.Round(time.Millisecond)
	if d == 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	ms := d / time.Millisecond % 1000
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	if ms > 0 {
		return fmt.Sprintf("%ds%03dms", s, ms)
	}
	return fmt.Sprintf("%ds", s)
}

func timeAnchorWait(s *config.Schedule, now time.Time) (time.Duration, bool) {
	if s == nil || s.Time.Empty() {
		return 0, false
	}
	next, ok := s.Time.Next(now)
	if !ok {
		return 0, false
	}
	return next.Sub(now), true
}

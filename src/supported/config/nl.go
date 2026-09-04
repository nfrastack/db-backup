// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package configsupporter

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
)

type nlResult struct {
	timeMin     int
	days        []int
	weekdayAll  bool
	weekendAll  bool
	intervalMin int
	beginMin    int
	haveTime    bool
}

func ParseNL(phrase string) (*config.Schedule, bool) {
	phrase = strings.ToLower(strings.TrimSpace(phrase))

	var r nlResult
	r.timeMin = -1
	haveFact := false

	if m := regexp.MustCompile(`\bin\s+(\d+)\s+minute`).FindStringSubmatch(phrase); m != nil {
		n, _ := strconv.Atoi(m[1])
		r.beginMin = n
		haveFact = true
	}
	if m := regexp.MustCompile(`\bafter\s+(\d+)\s+hour`).FindStringSubmatch(phrase); m != nil {
		n, _ := strconv.Atoi(m[1])
		r.beginMin = n * 60
		haveFact = true
	}
	if strings.HasPrefix(phrase, "+") {
		if n, err := strconv.Atoi(strings.TrimPrefix(phrase, "+")); err == nil && n > 0 {
			r.beginMin = n
			haveFact = true
		}
	}

	if m := regexp.MustCompile(`every\s+(\d+)\s+minute`).FindStringSubmatch(phrase); m != nil {
		n, _ := strconv.Atoi(m[1])
		r.intervalMin = n
		haveFact = true
	}
	if m := regexp.MustCompile(`every\s+(\d+)\s+hour`).FindStringSubmatch(phrase); m != nil {
		n, _ := strconv.Atoi(m[1])
		r.intervalMin = n * 60
		haveFact = true
	}
	if m := regexp.MustCompile(`every\s+(\d+)\s+day`).FindStringSubmatch(phrase); m != nil {
		n, _ := strconv.Atoi(m[1])
		r.intervalMin = n * 1440
		haveFact = true
	}
	if strings.Contains(phrase, "every minute") {
		r.intervalMin = 1
		haveFact = true
	}
	if strings.Contains(phrase, "every hour") {
		r.intervalMin = 60
		haveFact = true
	}
	if strings.Contains(phrase, "every day") {
		r.intervalMin = 1440
		haveFact = true
	}

	if strings.Contains(phrase, "midnight") {
		r.timeMin = 0
		r.haveTime = true
		haveFact = true
	}
	if strings.Contains(phrase, "noon") {
		r.timeMin = 12 * 60
		r.haveTime = true
		haveFact = true
	}

	if m := regexp.MustCompile(`(\d{1,2}):(\d{2})\s*(am|pm)?`).FindStringSubmatch(phrase); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		meridiem := m[3]
		if meridiem == "pm" && h < 12 {
			h += 12
		}
		if meridiem == "am" && h == 12 {
			h = 0
		}
		r.timeMin = h*60 + mi
		r.haveTime = true
		haveFact = true
	} else if m := regexp.MustCompile(`(\d{1,2})\s*(am|pm)`).FindStringSubmatch(phrase); m != nil {
		h, _ := strconv.Atoi(m[1])
		if m[2] == "pm" && h < 12 {
			h += 12
		}
		if m[2] == "am" && h == 12 {
			h = 0
		}
		r.timeMin = h * 60
		r.haveTime = true
		haveFact = true
	} else if m := regexp.MustCompile(`\b(\d{3,4})\b`).FindStringSubmatch(phrase); m != nil {
		v := m[1]
		if len(v) == 4 {
			h, _ := strconv.Atoi(v[:2])
			mi, _ := strconv.Atoi(v[2:])
			if h <= 23 && mi <= 59 {
				r.timeMin = h*60 + mi
				r.haveTime = true
				haveFact = true
			}
		}
	}

	dayNames := []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	daySeen := make(map[int]bool)
	for i, name := range dayNames {
		if strings.Contains(phrase, name) || strings.Contains(phrase, strings.TrimSuffix(name, "day")+"s") {
			daySeen[i] = true
			haveFact = true
		}
	}

	for i, name := range dayNames {
		short := name[:3]
		if strings.Contains(phrase, short) {
			daySeen[i] = true
			haveFact = true
		}
	}

	for _, part := range strings.Split(phrase, ",") {
		part = strings.TrimSpace(part)
		for i, name := range dayNames {
			if part == name[:3] || part == name || part == strings.TrimSuffix(name, "day") {
				daySeen[i] = true
				haveFact = true
			}
		}
	}
	if len(daySeen) > 0 {
		for d := range daySeen {
			r.days = append(r.days, d)
		}
		sort.Ints(r.days)
	}

	if strings.Contains(phrase, "weekday") || strings.Contains(phrase, "weekdays") {
		r.weekdayAll = true
		r.days = []int{1, 2, 3, 4, 5}
		haveFact = true
	}
	if strings.Contains(phrase, "weekend") || strings.Contains(phrase, "weekends") {
		r.weekendAll = true
		r.days = []int{0, 6}
		haveFact = true
	}

	dailyHint := strings.Contains(phrase, "daily") || strings.Contains(phrase, "nightly") ||
		strings.Contains(phrase, "every day")

	if !haveFact {
		return nil, false
	}

	s := &config.Schedule{}

	if r.beginMin > 0 && (r.haveTime || len(r.days) > 0 || r.intervalMin > 0) {
		s.Start = fmt.Sprintf("+%d", r.beginMin)

		if r.haveTime {
			hh := r.timeMin / 60
			mm := r.timeMin % 60
			if len(r.days) > 0 {
				s.Recurring = buildCron(hh, mm, r.days)
			} else {
				s.Recurring = fmt.Sprintf("%d %d * * *", mm, hh)
			}
			return s, true
		}
		if r.intervalMin > 0 {
			s.Recurring = fmt.Sprintf("%d", r.intervalMin)
			return s, true
		}
		s.Recurring = buildCron(0, 0, r.days)
		return s, true
	}

	if r.beginMin > 0 {
		s.Begin = fmt.Sprintf("+%d", r.beginMin)
		return s, true
	}

	if r.haveTime {
		hh := r.timeMin / 60
		mm := r.timeMin % 60
		s.Time = config.TimeList{r.timeMin}

		if len(r.days) > 0 {
			s.Cron = buildCron(hh, mm, r.days)
			return s, true
		}

		if r.intervalMin > 0 && r.intervalMin%60 == 0 {
			stepH := r.intervalMin / 60
			var hours []string
			for h := hh; h < 24; h += stepH {
				hours = append(hours, strconv.Itoa(h))
			}
			s.Cron = fmt.Sprintf("%d %s * * *", mm, strings.Join(hours, ","))
			return s, true
		}

		s.Cron = fmt.Sprintf("%d %d * * *", mm, hh)
		return s, true
	}

	if r.intervalMin > 0 {
		s.Interval = r.intervalMin
		return s, true
	}

	if len(r.days) > 0 {
		s.Cron = buildCron(0, 0, r.days)
		return s, true
	}

	if dailyHint {
		s.Cron = "0 0 * * *"
		return s, true
	}

	return nil, false
}
func buildCron(hh, mm int, days []int) string {
	dayStr := ""
	for i, d := range days {
		if i > 0 {
			dayStr += ","
		}
		dayStr += strconv.Itoa(d)
	}
	return fmt.Sprintf("%d %d * * %s", mm, hh, dayStr)
}

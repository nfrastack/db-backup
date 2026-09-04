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
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
)

var weekdayAliases = func() map[string][]time.Weekday {
	m := map[string][]time.Weekday{}
	dayNames := []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	for i, name := range dayNames {
		wk := time.Weekday(i)
		forms := []string{name, name[:3], strings.TrimSuffix(name, "day")}
		for _, f := range forms {
			m[f] = append(m[f], wk)
		}
	}
	m["thur"] = []time.Weekday{time.Thursday}
	m["tues"] = []time.Weekday{time.Tuesday}
	m["weekdays"] = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	m["weekend"] = []time.Weekday{time.Saturday, time.Sunday}
	m["all"] = []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
	m["any"] = m["all"]
	return m
}()

func blackoutDaySkip(b *config.Blackout, t time.Time) bool {
	return !b.Days.Empty() && !b.Days.Matches(t)
}
func dayAllowed(s *config.Schedule, t time.Time) bool {
	if !s.Days.Empty() && !s.Days.Matches(t) {
		return false
	}
	if !s.ExcludeDays.Empty() && s.ExcludeDays.Matches(t) {
		return false
	}
	return true
}

func firstWait(s *config.Schedule) (time.Duration, bool) {
	if s.Start == "" {
		return 0, false
	}
	return config.ParseOffset(s.Start)
}
func parseDayList(s string) (*config.DayListData, error) {
	data := &config.DayListData{
		Weekdays: map[time.Weekday]bool{},
		DOM:      map[int]bool{},
	}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if n, err := strconv.Atoi(tok); err == nil {
			if n < 1 || n > 31 {
				return nil, fmt.Errorf("day of month %d out of range (1-31)", n)
			}
			data.DOM[n] = true
			continue
		}
		days, ok := parseWeekday(tok)
		if !ok {
			return nil, fmt.Errorf("invalid day %q (use mon..sun, weekdays, weekend, all, or 1-31)", tok)
		}
		for _, wd := range days {
			data.Weekdays[wd] = true
		}
	}
	return data, nil
}
func parseWeekday(tok string) ([]time.Weekday, bool) {
	if days, ok := weekdayAliases[strings.ToLower(tok)]; ok {
		return days, true
	}
	return nil, false
}

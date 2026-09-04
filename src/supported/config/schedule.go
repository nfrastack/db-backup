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

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/license"
)

func init() {
	config.SupporterParseNL = parseNL
	config.SupporterSchedule = licensedSchedule
	config.SupporterParseDayList = parseDayList
	config.SupporterDayAllowed = dayAllowed
	config.SupporterFirstWait = firstWait
	config.SupporterBlackoutDaySkip = blackoutDaySkip
}

func licensedSchedule(s *config.Schedule) error {
	if err := license.AllowSchedule(); err != nil {
		return config.CommunityScheduleLimits(s)
	}
	return nil
}
func parseNL(phrase string) (*config.Schedule, bool, error) {
	if err := license.AllowSchedule(); err != nil {
		return nil, true, fmt.Errorf("natural-language schedule %q requires Supporter license (%s) - community supports cron, interval, single HHMM / HH:MM time, +MM begin and basic blackout HHMM window", phrase, err)
	}
	ns, ok := ParseNL(phrase)
	return ns, ok, nil
}

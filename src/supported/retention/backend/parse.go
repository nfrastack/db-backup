// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package backend

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/retention"
)

func init() {
	retention.SupporterParseWithin = parseWithinFull
}

func parseWithinFull(s string) (time.Duration, error) {
	if err := license.AllowRetention(); err != nil {
		return 0, fmt.Errorf("keep_within %q: %v", s, err)
	}
	num := s
	mult := time.Hour
	switch {
	case strings.HasSuffix(s, "s"):
		num = strings.TrimSuffix(s, "s")
		mult = time.Second
	case strings.HasSuffix(s, "m"):
		num = strings.TrimSuffix(s, "m")
		mult = time.Minute
	case strings.HasSuffix(s, "h"):
		num = strings.TrimSuffix(s, "h")
		mult = time.Hour
	case strings.HasSuffix(s, "d"):
		num = strings.TrimSuffix(s, "d")
		mult = 24 * time.Hour
	}
	n, err := strconv.Atoi(strings.TrimSpace(num))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid keep_within %q: expected Ns, Nm, Nh, or Nd (eg 90s, 10m, 24h, 7d)", s)
	}
	return time.Duration(n) * mult, nil
}

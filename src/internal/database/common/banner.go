// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"fmt"
	"time"

	"github.com/nfrastack/db-backup/internal/version"
)

const ProjectURL = "https://nfrastack.com/db-backup"

func DumpBanner(prefix, dbType, detail string) string {
	return DumpBannerAt(prefix, dbType, detail, time.Now().UTC())
}

func DumpBannerAt(prefix, dbType, detail string, now time.Time) string {
	return fmt.Sprintf("%s db-backup | %s | %s | %s\n%s Dumped: %s\n%s %s\n",
		prefix, dbType, ProjectURL, version.Display(),
		prefix, now.UTC().Format(time.RFC3339),
		prefix, detail)
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"fmt"
	"time"

	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/storage"
)

type ArchiveConfig struct {
	Last   int
	Within time.Duration
	Src    storage.Storage
	Dst    storage.Storage
}

var ArchiveRunner = func(cfg *ArchiveConfig) (int, []string, error) {
	return 0, nil, fmt.Errorf("archive is a Supporter feature not available in this build")
}

func CheckArchive() error {
	if err := license.AllowArchive(); err != nil {
		return fmt.Errorf("archive is not available in the Community edition (%s)", err)
	}
	return nil
}

func RunArchive(cfg *ArchiveConfig) (int, []string, error) {
	if err := CheckArchive(); err != nil {
		return 0, nil, err
	}
	return ArchiveRunner(cfg)
}

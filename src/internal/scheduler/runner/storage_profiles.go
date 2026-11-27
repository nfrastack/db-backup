// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/retention"
)

func multiStorageStub() error {
	return fmt.Errorf("multi-storage requires a Supporter license (not available in the Community edition)")
}

var UploadToProfiles = func(ctx context.Context, job config.JobConfig, filename string, r io.Reader, abort bool) (int64, []bool, string, error) {
	return 0, nil, "", multiStorageStub()
}

var WriteProfileSidecars = func(job config.JobConfig, filename string, sc *retention.Sidecar, succeeded []bool, abort bool) (string, error) {
	return "", multiStorageStub()
}

var LinkLatestProfiles = func(job config.JobConfig, dbName, filename string, succeeded []bool) {
}

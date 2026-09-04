// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package supported

import (
	_ "github.com/nfrastack/db-backup/supported/database/engine/mongo"
	_ "github.com/nfrastack/db-backup/supported/database/engine/mssql"
	_ "github.com/nfrastack/db-backup/supported/database/engine/mysql"
	_ "github.com/nfrastack/db-backup/supported/database/engine/postgres"
	_ "github.com/nfrastack/db-backup/supported/database/engine/redis"
)

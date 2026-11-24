// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import "github.com/nfrastack/db-backup/internal/database/objects"

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "sqlite",
		Aliases: []string{"sqlite3"},
		Objects: []string{
			"views", "triggers", "indexes", "sequences", "stats",
			"data", "drops",
		},
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

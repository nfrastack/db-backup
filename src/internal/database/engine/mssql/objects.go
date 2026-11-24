// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import "github.com/nfrastack/db-backup/internal/database/objects"

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "mssql",
		Aliases: []string{"microsoftsql"},
		Objects: []string{
			"routines", "triggers", "views", "indexes", "constraints",
			"data", "drops",
		},
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

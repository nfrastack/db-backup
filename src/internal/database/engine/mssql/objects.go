// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mssql

import "github.com/nfrastack/db-backup/internal/database/objects"

type MssqlObjects struct {
	Routines    bool
	Triggers    bool
	Views       bool
	Indexes     bool
	Constraints bool
	Data        bool
	Drops       bool
}

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

func ResolveObjects(o objects.Objects) MssqlObjects {
	return MssqlObjects{
		Routines:    o.Get("routines"),
		Triggers:    o.Get("triggers"),
		Views:       o.Get("views"),
		Indexes:     o.Get("indexes"),
		Constraints: o.Get("constraints"),
		Data:        o.Get("data"),
		Drops:       o.Get("drops"),
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

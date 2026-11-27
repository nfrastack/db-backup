// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite

import "github.com/nfrastack/db-backup/internal/database/objects"

type SqliteObjects struct {
	Views             bool
	Triggers          bool
	Indexes           bool
	Sequences         bool
	Stats             bool
	Data              bool
	Drops             bool
	SingleTransaction bool
}

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "sqlite",
		Aliases: []string{"sqlite3"},
		Objects: []string{
			"views", "triggers", "indexes", "sequences", "stats",
			"data", "drops",
		},
		Options: map[string]objects.OptionSpec{
			"single_transaction": {Type: "bool"},
		},
	}
}

func ResolveObjects(o objects.Objects, opts objects.Options) SqliteObjects {
	return SqliteObjects{
		Views:             o.Get("views"),
		Triggers:          o.Get("triggers"),
		Indexes:           o.Get("indexes"),
		Sequences:         o.Get("sequences"),
		Stats:             o.Get("stats"),
		Data:              o.Get("data"),
		Drops:             o.Get("drops"),
		SingleTransaction: opts.GetBool("single_transaction", true),
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

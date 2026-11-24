// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mysql

import (
	"strings"

	"github.com/nfrastack/db-backup/internal/database/objects"
)

type MysqlObjects struct {
	Routines bool
	Events   bool
	Triggers bool
	Views    bool
	RawBlobs bool
}

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "mysql",
		Aliases: []string{"mariadb"},
		Objects: []string{"routines", "events", "triggers", "views"},
		Options: map[string]objects.OptionSpec{
			"blob_format": {Type: "enum", Values: []string{"raw", "hex"}},
		},
	}
}

func ResolveObjects(o objects.Objects, opts objects.Options) MysqlObjects {
	return MysqlObjects{
		Routines: o.Get("routines"),
		Events:   o.Get("events"),
		Triggers: o.Get("triggers"),
		Views:    o.Get("views"),
		RawBlobs: strings.EqualFold(strings.TrimSpace(opts.Get("blob_format")), "raw"),
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

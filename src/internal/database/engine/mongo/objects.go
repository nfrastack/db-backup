// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import "github.com/nfrastack/db-backup/internal/database/objects"

type MongoObjects struct {
	Views             bool
	Indexes           bool
	CollectionOptions bool
	Data              bool
}

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "mongo",
		Aliases: []string{"mongodb"},
		Objects: []string{"views", "indexes", "collection_options", "data"},
	}
}

func ResolveObjects(o objects.Objects) MongoObjects {
	return MongoObjects{
		Views:             o.Get("views"),
		Indexes:           o.Get("indexes"),
		CollectionOptions: o.Get("collection_options"),
		Data:              o.Get("data"),
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

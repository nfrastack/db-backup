// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import "github.com/nfrastack/db-backup/internal/database/objects"

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "mongo",
		Aliases: []string{"mongodb"},
		Objects: []string{"views", "indexes", "collection_options", "data"},
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

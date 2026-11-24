// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package couch

import "github.com/nfrastack/db-backup/internal/database/objects"

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "couch",
		Aliases: []string{"couchdb"},
		Objects: []string{"attachments", "design_docs", "data"},
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import "github.com/nfrastack/db-backup/internal/database/objects"

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "influx",
		Objects: []string{"data"},
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

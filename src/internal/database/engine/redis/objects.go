// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import "github.com/nfrastack/db-backup/internal/database/objects"

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "redis",
		Objects: []string{"expires", "streams", "data"},
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

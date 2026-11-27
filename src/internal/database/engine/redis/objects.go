// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import "github.com/nfrastack/db-backup/internal/database/objects"

type RedisObjects struct {
	Expires bool
	Streams bool
	Data    bool
}

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "redis",
		Objects: []string{"expires", "streams", "data"},
	}
}

func ResolveObjects(o objects.Objects) RedisObjects {
	return RedisObjects{
		Expires: o.Get("expires"),
		Streams: o.Get("streams"),
		Data:    o.Get("data"),
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

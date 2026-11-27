// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package influx

import "github.com/nfrastack/db-backup/internal/database/objects"

type InfluxObjects struct {
	Data bool
}

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "influx",
		Objects: []string{"data"},
	}
}

func ResolveObjects(o objects.Objects) InfluxObjects {
	return InfluxObjects{
		Data: o.Get("data"),
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

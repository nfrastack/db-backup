// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package postgres

import (
	"strings"

	"github.com/nfrastack/db-backup/internal/database/objects"
)

type PostgresObjects struct {
	Functions         bool
	Aggregates        bool
	Triggers          bool
	Views             bool
	Matviews          bool
	Rules             bool
	Policies          bool
	Extensions        bool
	Types             bool
	Sequences         bool
	Indexes           bool
	Constraints       bool
	Comments          bool
	Blobs             bool
	Publications      bool
	Drops             bool
	Data              bool
	Owner             bool
	Acl               bool
	Collations        bool
	FailOnUnencodable bool
}

func ObjectsSpec() objects.Spec {
	return objects.Spec{
		Name:    "postgres",
		Aliases: []string{"pgsql", "postgresql"},
		Objects: []string{
			"functions", "aggregates", "triggers", "views", "matviews",
			"rules", "policies", "extensions", "types", "sequences",
			"indexes", "constraints", "comments", "blobs", "publications",
			"drops", "data", "owner", "acl", "collations",
		},
		Options: map[string]objects.OptionSpec{
			"fail_on_unencodable": {Type: "bool"},
		},
	}
}

func ResolveObjects(o objects.Objects, opts objects.Options) PostgresObjects {
	return PostgresObjects{
		Functions:         o.Get("functions"),
		Aggregates:        o.Get("aggregates"),
		Triggers:          o.Get("triggers"),
		Views:             o.Get("views"),
		Matviews:          o.Get("matviews"),
		Rules:             o.Get("rules"),
		Policies:          o.Get("policies"),
		Extensions:        o.Get("extensions"),
		Types:             o.Get("types"),
		Sequences:         o.Get("sequences"),
		Indexes:           o.Get("indexes"),
		Constraints:       o.Get("constraints"),
		Comments:          o.Get("comments"),
		Blobs:             o.Get("blobs"),
		Publications:      o.Get("publications"),
		Drops:             o.Get("drops"),
		Data:              o.Get("data"),
		Owner:             o.Get("owner"),
		Acl:               o.Get("acl"),
		Collations:        o.Get("collations"),
		FailOnUnencodable: strings.EqualFold(strings.TrimSpace(opts.Get("fail_on_unencodable")), "true"),
	}
}

func init() {
	objects.Register(ObjectsSpec())
}

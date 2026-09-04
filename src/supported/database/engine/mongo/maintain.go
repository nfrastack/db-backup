// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1
//
// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func init() {
	registry.RegisterMaintain(registry.MaintainSpec{Engine: "mongo", Run: maintain})
}

func maintain(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg, tlsCfg *config.TLSConfig) ([]common.OpResult, error) {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "admin"
	}
	uri := fmt.Sprintf("mongodb://%s:%s@%s:%d/?directConnection=true", user, pass, host, port)
	if authSource != "" {
		uri += "&authSource=" + authSource
	}
	if user == "" && pass == "" {
		uri = fmt.Sprintf("mongodb://%s:%d/?directConnection=true", host, port)
		if authSource != "" {
			uri += "&authSource=" + authSource
		}
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect(ctx)

	var results []common.OpResult

	if common.Enabled("compact", cfg) {
		r, start := common.StartOp("compact")
		collNames, err := client.Database(firstDB).ListCollectionNames(ctx, bson.D{})
		if err != nil {
			r.Status = "ERROR"
			r.Detail = err.Error()
		} else {
			for _, c := range collNames {
				if err := client.Database(firstDB).RunCommand(ctx, bson.D{{Key: "compact", Value: c}}).Err(); err != nil {
					r.Detail += fmt.Sprintf("%s: %v; ", c, err)
				}
			}
			if r.Detail == "" {
				r.Detail = fmt.Sprintf("%d collections", len(collNames))
			} else {
				r.Status = "ERROR"
			}
		}
		results = append(results, common.FinishOp(&r, start))
	}

	return results, nil
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package mongo

import (
	"context"
	"fmt"
	"io"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	enginemongo "github.com/nfrastack/db-backup/internal/database/engine/mongo"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func CheckSupport(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "admin"
	}
	uri := enginemongo.URI(user, pass, host, port, authSource, tlsCfg)
	if user == "" && pass == "" {
		uri = fmt.Sprintf("mongodb://%s:%d/?directConnection=true", host, port)
		if tlsCfg != nil && tlsCfg.Enable {
			uri += "&tls=true"
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect(ctx)

	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if hello.SetName == "" {
		return fmt.Errorf("mongod is not running as a replica set - change streams require --replSet")
	}
	return nil
}

func IncrementalDump(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, since, authSource string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "admin"
	}
	uri := enginemongo.URI(user, pass, host, port, authSource, tlsCfg)
	if user == "" && pass == "" {
		uri = fmt.Sprintf("mongodb://%s:%d/?directConnection=true", host, port)
		if tlsCfg != nil && tlsCfg.Enable {
			uri += "&tls=true"
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect(ctx)

	fmt.Fprintf(w, "// MongoDB Change Stream dump\n")
	fmt.Fprintf(w, "// Host: %s:%d  DB: %s\n//\n\n", host, port, firstDB)

	db := client.Database(firstDB)

	var cs *mongo.ChangeStream
	if since != "" {
		var resumeToken bson.Raw
		if err := bson.UnmarshalExtJSON([]byte(since), true, &resumeToken); err == nil {
			cs, err = db.Watch(ctx, mongo.Pipeline{}, options.ChangeStream().SetResumeAfter(resumeToken))
		}
	}
	if cs == nil {
		cs, err = db.Watch(ctx, mongo.Pipeline{})
	}
	if err != nil {
		return fmt.Errorf("change stream: %w", err)
	}
	defer cs.Close(ctx)

	limit := 100
	for i := 0; i < limit && cs.Next(ctx); i++ {
		var change bson.M
		if err := cs.Decode(&change); err != nil {
			continue
		}
		b, _ := bson.MarshalExtJSON(change, true, false)
		fmt.Fprintf(w, "// %s\n", string(b))

		op, _ := change["operationType"].(string)
		ns, _ := change["ns"].(bson.M)
		coll, _ := ns["coll"].(string)
		fullDoc, _ := change["fullDocument"].(bson.M)

		if coll != "" && (op == "insert" || op == "replace") && fullDoc != nil {
			docJSON, _ := bson.MarshalExtJSON(fullDoc, true, false)
			fmt.Fprintf(w, "db.%s.insert(%s)\n", coll, string(docJSON))
		}
	}

	return cs.Err()
}

func IncrementalSpec() registry.IncrementalSpec {
	return registry.IncrementalSpec{
		Engine: "mongo",
		CheckSupport: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return CheckSupport(ctx, host, port, user, pass, dbName, authSource, tlsCfg)
		},
		GetPosition: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
			return Position(ctx, host, port, user, pass, dbName, authSource, tlsCfg)
		},
		Dump: func(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error {
			return IncrementalDump(ctx, w, host, port, user, pass, dbName, since, authSource, tlsCfg)
		},
	}
}
func Position(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "admin"
	}
	uri := enginemongo.URI(user, pass, host, port, authSource, tlsCfg)
	if user == "" && pass == "" {
		uri = fmt.Sprintf("mongodb://%s:%d/?directConnection=true", host, port)
		if tlsCfg != nil && tlsCfg.Enable {
			uri += "&tls=true"
		}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect(ctx)

	var result bson.M
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "collStats", Value: "oplog.rs"}}).Decode(&result)
	if err != nil {
		return "", fmt.Errorf("get oplog stats: %w", err)
	}

	var lastEntry bson.M
	cursor, err := client.Database("local").Collection("oplog.rs").Find(ctx,
		bson.D{}, options.Find().SetSort(bson.D{{Key: "$natural", Value: -1}}).SetLimit(1))
	if err != nil {
		return "", fmt.Errorf("query oplog: %w", err)
	}
	defer cursor.Close(ctx)

	if cursor.Next(ctx) {
		cursor.Decode(&lastEntry)
		ts, ok := lastEntry["ts"].(primitive.Timestamp)
		if ok {
			token := bson.D{{Key: "$clusterTime", Value: bson.D{
				{Key: "clusterTime", Value: ts},
			}}}
			b, _ := bson.MarshalExtJSON(token, true, false)
			return string(b), nil
		}
	}
	return "", nil
}
func init() {
	registry.RegisterIncremental(IncrementalSpec())
}

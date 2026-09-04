// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ListDatabases(host string, port int, user, pass, authSource string, tlsCfg *config.TLSConfig) ([]string, error) {
	uri := URI(user, pass, host, port, authSource, tlsCfg)
	if user == "" && pass == "" {
		uri = fmt.Sprintf("mongodb://%s:%d/?directConnection=true", host, port)
		if tlsCfg != nil && tlsCfg.Enable {
			uri += "&tls=true"
		}
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect(ctx)

	names, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}

	var dbs []string
	for _, n := range names {
		if !isSystemDB(n) {
			dbs = append(dbs, n)
		}
	}
	return dbs, nil
}

func Maintain(host string, port int, user, pass, dbName, authSource string, cfg *common.MaintenanceCfg) ([]common.OpResult, error) {
	return nil, nil
}

func Restore(r io.Reader, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
	firstDB := common.FirstDBName(dbName)
	if firstDB == "" {
		firstDB = "admin"
	}
	uri := URI(user, pass, host, port, authSource, tlsCfg)
	if user == "" && pass == "" {
		uri = fmt.Sprintf("mongodb://%s:%d/?directConnection=true", host, port)
		if tlsCfg != nil && tlsCfg.Enable {
			uri += "&tls=true"
		}
	}

	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect(ctx)
	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	scanner := bufio.NewScanner(r)
	var collection string
	var docs []bson.M
	inInsert := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		if strings.HasPrefix(line, "db.") && strings.Contains(line, ".insertMany([") {
			collection = extractCollection(line)
			inInsert = true
			docs = nil
			continue
		}

		if inInsert {
			if strings.HasPrefix(line, "]);") || line == "]);" {
				inInsert = false
				if len(docs) > 0 {
					entries := make([]interface{}, len(docs))
					for i := range docs {
						entries[i] = docs[i]
					}
					if _, err := client.Database(firstDB).Collection(collection).InsertMany(ctx, entries); err != nil {
						return fmt.Errorf("insert %s: %w", collection, err)
					}
				}
				continue
			}
			line = strings.TrimSuffix(line, ",")
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var doc bson.M
			if err := json.Unmarshal([]byte(line), &doc); err != nil {
				return fmt.Errorf("parse doc: %w", err)
			}
			docs = append(docs, doc)
		}
	}
	return scanner.Err()
}

func extractCollection(line string) string {
	rest := strings.TrimPrefix(line, "db.")
	idx := strings.Index(rest, ".insertMany")
	if idx < 0 {
		return "unknown"
	}
	return rest[:idx]
}

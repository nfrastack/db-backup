// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

const (
	mongoMaxLineBytes     = 64 * 1024 * 1024
	mongoRestoreBatchSize = 1000
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
	scanner.Buffer(make([]byte, 64*1024), mongoMaxLineBytes)
	var collection string
	var lineNo int
	batcher := &docBatcher{size: mongoRestoreBatchSize}
	inInsert := false
	insert := func(docs []bson.M) error {
		if len(docs) == 0 {
			return nil
		}
		if _, err := client.Database(firstDB).Collection(collection).InsertMany(ctx, toInsertEntries(docs)); err != nil {
			return fmt.Errorf("insert %s: %w", collection, err)
		}
		return nil
	}

	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		if strings.HasPrefix(line, "db.") && strings.Contains(line, ".createCollection(") {
			if err := createCollectionLine(ctx, client, firstDB, line); err != nil {
				return err
			}
			continue
		}

		if strings.HasPrefix(line, "db.") && strings.Contains(line, ".createView(") {
			if err := createViewLine(ctx, client, firstDB, line); err != nil {
				return err
			}
			continue
		}

		if strings.HasPrefix(line, "db.") && strings.Contains(line, ".createIndexes([") {
			if err := createIndexesLine(ctx, client, firstDB, line); err != nil {
				return err
			}
			continue
		}

		if strings.HasPrefix(line, "db.") && strings.Contains(line, ".insertMany([") {
			collection = extractCollection(line)
			inInsert = true
			batcher.take()
			continue
		}

		if inInsert {
			if strings.HasPrefix(line, "]);") || line == "]);" {
				inInsert = false
				if err := insert(batcher.take()); err != nil {
					return err
				}
				continue
			}
			doc, ok, err := parseDocLine(line)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if full := batcher.add(doc); full != nil {
				if err := insert(full); err != nil {
					return err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if collection == "" {
			return fmt.Errorf("read dump at line %d: %w", lineNo, err)
		}
		return fmt.Errorf("read dump at line %d (collection %s): %w", lineNo, collection, err)
	}
	return nil
}

type docBatcher struct {
	docs []bson.M
	size int
}

func (b *docBatcher) add(doc bson.M) []bson.M {
	b.docs = append(b.docs, doc)
	if len(b.docs) >= b.size {
		return b.take()
	}
	return nil
}

func (b *docBatcher) take() []bson.M {
	if len(b.docs) == 0 {
		return nil
	}
	full := b.docs
	b.docs = nil
	return full
}

func parseCreateCollectionLine(line string) (string, bson.M, error) {
	rest := strings.TrimPrefix(line, "db.")
	idx := strings.Index(rest, ".createCollection(")
	if idx < 0 {
		return "", nil, fmt.Errorf("bad createCollection line")
	}
	collection := rest[:idx]
	payload := rest[idx+len(".createCollection("):]
	if !strings.HasSuffix(payload, ")") {
		return "", nil, fmt.Errorf("bad createCollection line")
	}
	var opts bson.M
	if err := bson.UnmarshalExtJSON([]byte(payload[:len(payload)-1]), false, &opts); err != nil {
		return "", nil, fmt.Errorf("parse collection options: %w", err)
	}
	return collection, opts, nil
}

func createCollectionLine(ctx context.Context, client *mongo.Client, db, line string) error {
	collection, opts, err := parseCreateCollectionLine(line)
	if err != nil {
		return err
	}
	if len(opts) == 0 {
		return nil
	}
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cmd := bson.D{{Key: "create", Value: collection}}
	for _, k := range keys {
		cmd = append(cmd, bson.E{Key: k, Value: opts[k]})
	}
	return client.Database(db).RunCommand(ctx, cmd).Err()
}

func parseCreateViewLine(line string) (string, string, any, error) {
	rest := strings.TrimPrefix(line, "db.")
	idx := strings.Index(rest, ".createView(")
	if idx < 0 {
		return "", "", nil, fmt.Errorf("bad createView line")
	}
	view := rest[:idx]
	payload := rest[idx+len(".createView("):]
	if !strings.HasSuffix(payload, ")") {
		return "", "", nil, fmt.Errorf("bad createView line")
	}
	var spec bson.M
	if err := bson.UnmarshalExtJSON([]byte(payload[:len(payload)-1]), false, &spec); err != nil {
		return "", "", nil, fmt.Errorf("parse view: %w", err)
	}
	viewOn, _ := spec["viewOn"].(string)
	if viewOn == "" {
		return "", "", nil, fmt.Errorf("bad createView line")
	}
	return view, viewOn, spec["pipeline"], nil
}

func createViewLine(ctx context.Context, client *mongo.Client, db, line string) error {
	view, viewOn, pipeline, err := parseCreateViewLine(line)
	if err != nil {
		return err
	}
	return client.Database(db).RunCommand(ctx, bson.D{{Key: "create", Value: view}, {Key: "viewOn", Value: viewOn}, {Key: "pipeline", Value: pipeline}}).Err()
}

func parseCreateIndexesLine(line string) (string, []bson.M, error) {
	rest := strings.TrimPrefix(line, "db.")
	idx := strings.Index(rest, ".createIndexes([")
	if idx < 0 {
		return "", nil, fmt.Errorf("bad createIndexes line")
	}
	collection := rest[:idx]
	payload := rest[idx+len(".createIndexes(["):]
	if !strings.HasSuffix(payload, ")") {
		return "", nil, fmt.Errorf("bad createIndexes line")
	}
	var specs []bson.M
	if err := bson.UnmarshalExtJSON([]byte("["+strings.TrimSuffix(payload, ")")), false, &specs); err != nil {
		return "", nil, fmt.Errorf("parse indexes: %w", err)
	}
	return collection, specs, nil
}

func createIndexesLine(ctx context.Context, client *mongo.Client, db, line string) error {
	collection, specs, err := parseCreateIndexesLine(line)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return nil
	}
	return client.Database(db).RunCommand(ctx, bson.D{{Key: "createIndexes", Value: collection}, {Key: "indexes", Value: specs}}).Err()
}

func extractCollection(line string) string {
	rest := strings.TrimPrefix(line, "db.")
	idx := strings.Index(rest, ".insertMany")
	if idx < 0 {
		return "unknown"
	}
	return rest[:idx]
}

func parseDocLine(line string) (doc bson.M, ok bool, err error) {
	line = strings.TrimSuffix(line, ",")
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, false, nil
	}
	var d bson.M
	if err := bson.UnmarshalExtJSON([]byte(line), false, &d); err != nil {
		return nil, false, fmt.Errorf("parse doc: %w", err)
	}
	return d, true, nil
}

func toInsertEntries(docs []bson.M) []interface{} {
	entries := make([]interface{}, len(docs))
	for i := range docs {
		entries[i] = docs[i]
	}
	return entries
}

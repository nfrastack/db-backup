// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

type Dumper struct {
	host       string
	port       int
	user       string
	pass       string
	dbName     string
	authSource string
	uri        string
	client     *mongo.Client
	serverVer  string
	tlsCfg     *config.TLSConfig
	connCfg    *config.ConnectivityConfig
	ctx        context.Context
	Tables     *config.TableFilter
	SchemaOnly bool
}

func (d *Dumper) Close() error {
	if d.client != nil {
		return d.client.Disconnect(d.ctxOrBg())
	}
	return nil
}

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	ctx := d.ctxOrBg()

	fmt.Fprintf(w, "// dbbackup MongoDB dump\n")
	fmt.Fprintf(w, "// Host: %s  Server: %s\n//\n\n", d.host, d.serverVer)

	if len(dbNames) == 1 && strings.ToLower(dbNames[0]) == "all" {
		databases, err := d.client.ListDatabaseNames(ctx, bson.D{})
		if err != nil {
			return fmt.Errorf("list databases: %w", err)
		}
		dbNames = nil
		for _, db := range databases {
			if !isSystemDB(db) {
				dbNames = append(dbNames, db)
			}
		}
	}

	for _, dbName := range dbNames {
		if err := d.dumpDatabase(ctx, w, dbName); err != nil {
			return fmt.Errorf("dump %s: %w", dbName, err)
		}
	}

	return nil
}

func NewDumper(host string, port int, user, pass, dbName, authSource string, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 27017
	}
	d := &Dumper{
		host:       host,
		port:       port,
		user:       user,
		pass:       pass,
		dbName:     dbName,
		authSource: authSource,
	}
	if len(tlsCfg) > 0 && tlsCfg[0] != nil {
		d.tlsCfg = tlsCfg[0]
	}
	return d
}

func (d *Dumper) Open() error {
	return d.OpenContext(context.Background())
}

func (d *Dumper) OpenContext(ctx context.Context) error {
	d.ctx = ctx
	probe := func() error { return common.TCPDial(d.host, d.port) }
	connect := func() error {
		uri := d.uri
		if uri == "" {
			uri = URI(d.user, d.pass, d.host, d.port, d.authSource, d.tlsCfg)
		}
		client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		d.client = client
		return nil
	}
	ping := func() error {
		pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := d.client.Ping(pingCtx, nil); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		var bi bson.Raw
		if err := d.client.Database("admin").RunCommand(pingCtx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&bi); err == nil {
			if v, ok := bi.Lookup("version").StringValueOK(); ok {
				d.serverVer = v
			}
		}
		return nil
	}
	return common.WithConnectivity(ctx, "mongo", d.connCfg, probe, connect, ping)
}

func (d *Dumper) SetConnectivity(cfg *config.ConnectivityConfig) {
	if cfg != nil {
		d.connCfg = cfg
	}
}

func (d *Dumper) SetTableFilter(f *config.TableFilter, schemaOnly bool) {
	d.Tables = f
	d.SchemaOnly = schemaOnly
}

func bsonToJSON(doc bson.M) string {
	var sb strings.Builder
	sb.WriteString("{")
	first := true
	for k, v := range doc {
		if first {
			first = false
		} else {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("\"%s\": %s", k, mongoValue(v)))
	}
	sb.WriteString("}")
	return sb.String()
}
func (d *Dumper) ctxOrBg() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

func (d *Dumper) dumpCollection(ctx context.Context, w io.Writer, dbName, col string) error {
	coll := d.client.Database(dbName).Collection(col)

	cursor, err := coll.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "$natural", Value: 1}}))
	if err != nil {
		return fmt.Errorf("find %s: %w", col, err)
	}
	defer cursor.Close(ctx)

	fmt.Fprintf(w, "// Collection: %s\n", col)

	var docCount int
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return fmt.Errorf("decode: %w", err)
		}

		json := bsonToJSON(doc)
		if docCount == 0 {
			fmt.Fprintf(w, "db.%s.insertMany([\n", col)
		} else {
			fmt.Fprintf(w, ",\n")
		}
		fmt.Fprintf(w, "  %s", json)
		docCount++
	}

	if docCount > 0 {
		fmt.Fprintf(w, "\n]);\n\n")
	}

	return cursor.Err()
}

func (d *Dumper) dumpDatabase(ctx context.Context, w io.Writer, dbName string) error {
	fmt.Fprintf(w, "\n// Database: %s\n\n", dbName)

	collections, err := d.client.Database(dbName).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}

	for _, col := range collections {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(col)
			if !included {
				continue
			}
		}
		common.TraceTable(ctx, dbName, col)
		if err := d.dumpCollection(ctx, w, dbName, col); err != nil {
			return err
		}
	}

	return nil
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

func isSystemDB(name string) bool {
	switch name {
	case "admin", "config", "local":
		return true
	}
	return false
}

func mongoValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("\"%s\"", escapeJSON(val))
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int, int32, int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%v", val)
	case primitive.ObjectID:
		return fmt.Sprintf("ObjectId(\"%s\")", val.Hex())
	case primitive.DateTime:
		t := time.UnixMilli(int64(val))
		return fmt.Sprintf("ISODate(\"%s\")", t.Format("2006-01-02T15:04:05.000Z"))
	case primitive.A:
		var items []string
		for _, item := range val {
			items = append(items, mongoValue(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case primitive.M:
		return bsonToJSON(bson.M(val))
	case map[string]any:
		return bsonToJSON(val)
	case []any:
		var items []string
		for _, item := range val {
			items = append(items, mongoValue(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		return fmt.Sprintf("\"%v\"", escapeJSON(fmt.Sprintf("%v", v)))
	}
}

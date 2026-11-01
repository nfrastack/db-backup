// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package mongo

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
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
	server     common.ServerVersion
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
	start := time.Now()
	log.Debug("mongo", "backup start",
		"host", d.host, "port", d.port, "server", d.serverVer,
		"databases", strings.Join(dbNames, ","))

	fmt.Fprint(w, common.DumpBanner("//", "MongoDB",
		fmt.Sprintf("Host: %s  Server: %s", d.host, d.server.Display())))
	fmt.Fprintf(w, "//\n\n")

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

	log.Debug("mongo", "backup done",
		"databases", len(dbNames),
		"elapsed", time.Since(start).Round(time.Millisecond).String())
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
	log.Debug("mongo", "connect start",
		"host", d.host, "port", d.port, "user", d.user,
		"auth_source", d.authSource, "tls", d.tlsCfg != nil)
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
				d.server = ParseServerVersion(v)
			}
		}
		log.Debug("mongo", "connected",
			"host", d.host, "port", d.port, "server", d.serverVer)
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

func (d *Dumper) ctxOrBg() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

func (d *Dumper) dumpCollectionOptions(w io.Writer, dbName string, col collectionInfo) error {
	if len(col.options) == 0 {
		return nil
	}
	spec := bson.M{}
	for _, k := range []string{"capped", "size", "max", "validator", "validationLevel", "validationAction", "timeseries", "changeStreamPreAndPostImages", "clusteredIndex"} {
		if v, ok := col.options[k]; ok {
			spec[k] = v
		}
	}
	if len(spec) == 0 {
		return nil
	}
	raw, err := bson.MarshalExtJSON(spec, true, false)
	if err != nil {
		return fmt.Errorf("encode collection options %s: %w", col.name, err)
	}
	fmt.Fprintf(w, "db.%s.createCollection(%s)\n", col.name, string(raw))
	return nil
}

func (d *Dumper) dumpCollection(ctx context.Context, w io.Writer, dbName, col string) error {
	coll := d.client.Database(dbName).Collection(col)

	cursor, err := coll.Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "$natural", Value: 1}}))
	if err != nil {
		return fmt.Errorf("find %s: %w", col, err)
	}
	defer cursor.Close(ctx)

	fmt.Fprintf(w, "// Collection: %s\n", col)
	colStart := time.Now()

	var docCount int
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return fmt.Errorf("decode: %w", err)
		}

		raw, err := bson.MarshalExtJSON(doc, true, false)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		json := string(raw)
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
	if err := cursor.Err(); err != nil {
		return err
	}
	if err := d.dumpIndexes(ctx, w, dbName, col); err != nil {
		return err
	}
	log.Trace("mongo", "collection done",
		"database", dbName, "collection", col, "docs", docCount,
		"elapsed", time.Since(colStart).Round(time.Millisecond).String())
	return nil
}

func (d *Dumper) dumpIndexes(ctx context.Context, w io.Writer, dbName, col string) error {
	cursor, err := d.client.Database(dbName).Collection(col).Indexes().List(ctx)
	if err != nil {
		log.Warn("mongo", "skipping indexes - list failed", "database", dbName, "collection", col, "error", err.Error())
		return nil
	}
	defer cursor.Close(ctx)
	var specs []string
	for cursor.Next(ctx) {
		var spec bson.M
		if err := cursor.Decode(&spec); err != nil {
			return fmt.Errorf("decode index %s: %w", col, err)
		}
		if name, _ := spec["name"].(string); name == "_id_" {
			continue
		}
		delete(spec, "ns")
		delete(spec, "v")
		raw, err := bson.MarshalExtJSON(spec, true, false)
		if err != nil {
			return fmt.Errorf("encode index %s: %w", col, err)
		}
		specs = append(specs, string(raw))
	}
	if err := cursor.Err(); err != nil {
		return err
	}
	if len(specs) == 0 {
		return nil
	}
	fmt.Fprintf(w, "db.%s.createIndexes([%s])\n\n", col, strings.Join(specs, ", "))
	return nil
}

type collectionInfo struct {
	name, kind string
	options    bson.M
}

func (d *Dumper) listCollections(ctx context.Context, dbName string) ([]collectionInfo, error) {
	cursor, err := d.client.Database(dbName).ListCollections(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var out []collectionInfo
	for cursor.Next(ctx) {
		var m bson.M
		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("decode collection: %w", err)
		}
		info := collectionInfo{}
		info.name, _ = m["name"].(string)
		info.kind, _ = m["type"].(string)
		info.options, _ = m["options"].(bson.M)
		if info.name == "" || strings.HasPrefix(info.name, "system.") {
			continue
		}
		out = append(out, info)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func (d *Dumper) dumpView(w io.Writer, dbName string, col collectionInfo) error {
	viewOn, _ := col.options["viewOn"].(string)
	if viewOn == "" {
		log.Warn("mongo", "skipping view - no viewOn", "database", dbName, "view", col.name)
		return nil
	}
	spec := bson.M{"viewOn": viewOn, "pipeline": col.options["pipeline"]}
	raw, err := bson.MarshalExtJSON(spec, true, false)
	if err != nil {
		return fmt.Errorf("encode view %s: %w", col.name, err)
	}
	fmt.Fprintf(w, "// View: %s\n", col.name)
	fmt.Fprintf(w, "db.%s.createView(%s)\n\n", col.name, string(raw))
	return nil
}

func (d *Dumper) dumpDatabase(ctx context.Context, w io.Writer, dbName string) error {
	dbStart := time.Now()
	fmt.Fprintf(w, "\n// Database: %s\n\n", dbName)

	collections, err := d.listCollections(ctx, dbName)
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	log.Debug("mongo", "dumping database",
		"database", dbName, "collections", len(collections))

	for _, col := range collections {
		if d.Tables != nil {
			included, _ := d.Tables.Apply(col.name)
			if !included {
				log.Trace("mongo", "collection excluded by filter",
					"database", dbName, "collection", col.name)
				continue
			}
		}
		common.TraceTable(ctx, dbName, col.name)
		if col.kind == "view" {
			if err := d.dumpView(w, dbName, col); err != nil {
				return err
			}
			continue
		}
		if err := d.dumpCollectionOptions(w, dbName, col); err != nil {
			return err
		}
		if err := d.dumpCollection(ctx, w, dbName, col.name); err != nil {
			return err
		}
	}

	log.Debug("mongo", "database done",
		"database", dbName, "collections", len(collections),
		"elapsed", time.Since(dbStart).Round(time.Millisecond).String())
	return nil
}

func isSystemDB(name string) bool {
	switch name {
	case "admin", "config", "local":
		return true
	}
	return false
}

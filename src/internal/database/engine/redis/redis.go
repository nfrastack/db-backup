// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import (
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
)

type Dumper struct {
	host       string
	port       int
	pass       string
	db         int
	client     *redis.Client
	tlsCfg     *config.TLSConfig
	connCfg    *config.ConnectivityConfig
	ctx        context.Context
	Tables     *config.TableFilter
	SchemaOnly bool
}

func (d *Dumper) Close() error {
	if d.client == nil {
		return nil
	}
	return d.client.Close()
}

func (d *Dumper) Dump(w io.Writer, dbNames []string) error {
	ctx := d.ctxOrBg()
	start := time.Now()
	if err := checkDBNames(dbNames, d.db); err != nil {
		return err
	}
	log.Debug("redis", "backup start",
		"host", d.host, "port", d.port, "tls", d.tlsCfg != nil,
		"auth", d.authMode(), "db", d.db)

	fmt.Fprint(w, common.DumpBanner("#", "Redis",
		fmt.Sprintf("Host: %s:%d", d.host, d.port)))
	fmt.Fprintf(w, "# Database: %d\n#\n\n", d.db)
	var cursor uint64
	var scanned, dumped, skipped int
	var skippedKeys []string
	for {
		keys, next, err := d.client.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		log.Trace("redis", "scan page",
			"cursor", cursor, "keys", len(keys), "next", next)
		scanned += len(keys)
		for _, key := range keys {
			if d.Tables != nil {
				included, _ := d.Tables.Apply(key)
				if !included {
					log.Trace("redis", "key excluded by filter", "key", key)
					continue
				}
			}
			common.TraceTable(ctx, "", key)
			if err := d.dumpKey(ctx, w, key); err != nil {
				skipped++
				skippedKeys = append(skippedKeys, key)
				log.Warn("redis", "skipping key - dump failed",
					"key", key, "error", err.Error())
				continue
			}
			dumped++
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	log.Debug("redis", "backup done",
		"scanned", scanned, "dumped", dumped, "skipped", skipped,
		"elapsed", time.Since(start).Round(time.Millisecond).String())
	if skipped > 0 {
		return fmt.Errorf("redis: backup incomplete - %d of %d keys skipped (%s)",
			skipped, scanned, strings.Join(skippedKeys, ", "))
	}
	return nil
}

func checkDBNames(dbNames []string, selected int) error {
	var names []string
	for _, n := range dbNames {
		if strings.TrimSpace(n) != "" {
			names = append(names, n)
		}
	}
	if len(names) > 1 {
		return fmt.Errorf("redis supports a single database index per backup, got %q", strings.Join(names, ","))
	}
	if len(names) == 0 {
		return nil
	}
	idx, err := ParseDBIndex(names[0])
	if err != nil {
		return err
	}
	if idx != selected {
		return fmt.Errorf("redis database mismatch: requested %d but connected to %d", idx, selected)
	}
	return nil
}

func NewDumper(host string, port int, pass string, tlsCfg ...*config.TLSConfig) *Dumper {
	if port == 0 {
		port = 6379
	}
	d := &Dumper{host: host, port: port, pass: pass}
	if len(tlsCfg) > 0 && tlsCfg[0] != nil {
		d.tlsCfg = tlsCfg[0]
	}
	return d
}

func ParseDBIndex(name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(name)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid redis database %q: must be a numeric database index (e.g. --name 3)", name)
	}
	return n, nil
}

func strconvFormatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (d *Dumper) Open() error {
	return d.OpenContext(context.Background())
}

func (d *Dumper) OpenContext(ctx context.Context) error {
	d.ctx = ctx
	log.Debug("redis", "connect start",
		"host", d.host, "port", d.port, "tls", d.tlsCfg != nil,
		"auth", d.authMode())
	probe := func() error { return common.TCPDial(d.host, d.port) }
	connect := func() error {
		opts := &redis.Options{
			Addr:        net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port)),
			Password:    d.pass,
			DB:          d.db,
			DialTimeout: 10 * time.Second,
		}
		if tc, err := common.BuildTLSConfig(d.tlsCfg); err == nil && tc != nil {
			opts.TLSConfig = tc
		}
		d.client = redis.NewClient(opts)
		return nil
	}
	ping := func() error {
		if err := d.client.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("ping: %w", err)
		}
		log.Debug("redis", "connected",
			"host", d.host, "port", d.port, "tls", d.tlsCfg != nil,
			"auth", d.authMode())
		return nil
	}
	return common.WithConnectivity(ctx, "redis", d.connCfg, probe, connect, ping)
}

func (d *Dumper) authMode() string {
	if d.pass == "" {
		return "none"
	}
	return "password"
}

func QuoteRedis(s string) string {
	return fmt.Sprintf("\"%s\"", strings.ReplaceAll(s, "\"", "\\\""))
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
func (d *Dumper) dumpKey(ctx context.Context, w io.Writer, key string) error {
	ttl, err := d.client.TTL(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("ttl: %w", err)
	}
	if ttl < 0 {
		ttl = 0
	}
	typ, err := d.client.Type(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("type: %w", err)
	}
	qKey := QuoteRedis(key)
	log.Trace("redis", "dumping key", "key", key, "type", typ, "ttl", ttl.String())
	switch typ {
	case "string":
		val, err := d.client.Get(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("get: %w", err)
		}
		d.writeWithTTL(w, qKey, val, ttl)
	case "list":
		vals, err := d.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			return fmt.Errorf("lrange: %w", err)
		}
		d.writeRestoreList(w, qKey, vals, ttl)
	case "set":
		vals, err := d.client.SMembers(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("smembers: %w", err)
		}
		d.writeRestoreSet(w, qKey, vals, ttl)
	case "hash":
		vals, err := d.client.HGetAll(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("hgetall: %w", err)
		}
		d.writeRestoreHash(w, qKey, vals, ttl)
	case "zset":
		vals, err := d.client.ZRangeWithScores(ctx, key, 0, -1).Result()
		if err != nil {
			return fmt.Errorf("zrange: %w", err)
		}
		d.writeRestoreZSet(w, qKey, vals, ttl)
	case "":
		log.Trace("redis", "key vanished mid-scan", "key", key)
		return nil
	case "stream":
		msgs, err := d.xRangeAll(ctx, key)
		if err != nil {
			return fmt.Errorf("xrange: %w", err)
		}
		groups, err := d.client.XInfoGroups(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("xinfo groups: %w", err)
		}
		d.writeRestoreStream(w, qKey, msgs, groups, ttl)
	default:

		val, err := d.client.Get(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("get fallback: %w", err)
		}
		d.writeWithTTL(w, qKey, val, ttl)
	}
	return nil
}

func (d *Dumper) getKeyValue(ctx context.Context, key string) (string, error) {
	typ, err := d.client.Type(ctx, key).Result()
	if err != nil {
		return "", err
	}
	switch typ {
	case "string":
		return d.client.Get(ctx, key).Result()
	case "list":
		vals, _ := d.client.LRange(ctx, key, 0, -1).Result()
		return "[" + strings.Join(vals, ", ") + "]", nil
	case "set":
		vals, _ := d.client.SMembers(ctx, key).Result()
		return "{" + strings.Join(vals, ", ") + "}", nil
	case "hash":
		vals, _ := d.client.HGetAll(ctx, key).Result()
		var parts []string
		for k, v := range vals {
			parts = append(parts, fmt.Sprintf("%s: %s", k, v))
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		return d.client.Get(ctx, key).Result()
	}
}

func (d *Dumper) writeRestoreCmd(w io.Writer, parts []string, ttl time.Duration) {
	fmt.Fprintln(w, strings.Join(parts, " "))
	writeKeyTTL(w, parts[1], ttl)
}

func writeKeyTTL(w io.Writer, qKey string, ttl time.Duration) {
	switch ttlSec := int64(ttl.Seconds()); {
	case ttlSec > 0:
		fmt.Fprintf(w, "EXPIRE %s %d\n", qKey, ttlSec)
	case ttl > 0:
		fmt.Fprintf(w, "PEXPIREAT %s %d\n", qKey, time.Now().Add(ttl).UnixMilli())
	}
}
func (d *Dumper) writeRestoreStream(w io.Writer, qKey string, msgs []redis.XMessage, groups []redis.XInfoGroup, ttl time.Duration) {
	for _, m := range msgs {
		parts := make([]string, 0, len(m.Values)*2+3)
		parts = append(parts, "XADD", qKey, QuoteRedis(m.ID))
		keys := make([]string, 0, len(m.Values))
		for k := range m.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sv, _ := m.Values[k].(string)
			if sv == "" {
				sv = fmt.Sprintf("%v", m.Values[k])
			}
			parts = append(parts, QuoteRedis(k), QuoteRedis(sv))
		}
		fmt.Fprintln(w, strings.Join(parts, " "))
	}
	for _, g := range groups {
		fmt.Fprintf(w, "XGROUP CREATE %s %s %s MKSTREAM\n",
			qKey, QuoteRedis(g.Name), QuoteRedis(g.LastDeliveredID))
	}
	writeKeyTTL(w, qKey, ttl)
}

func (d *Dumper) xRangeAll(ctx context.Context, key string) ([]redis.XMessage, error) {
	var out []redis.XMessage
	start := "-"
	for {
		msgs, err := d.client.XRangeN(ctx, key, start, "+", 1000).Result()
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			break
		}
		out = append(out, msgs...)
		if len(msgs) < 1000 {
			break
		}
		start = "(" + msgs[len(msgs)-1].ID
	}
	return out, nil
}
func (d *Dumper) writeRestoreHash(w io.Writer, qKey string, entries map[string]string, ttl time.Duration) {
	if len(entries) == 0 {
		return
	}
	parts := make([]string, 0, len(entries)*2+2)
	parts = append(parts, "HSET", qKey)
	for k, v := range entries {
		parts = append(parts, QuoteRedis(k), QuoteRedis(v))
	}
	d.writeRestoreCmd(w, parts, ttl)
}
func (d *Dumper) writeRestoreList(w io.Writer, qKey string, vals []string, ttl time.Duration) {
	if len(vals) == 0 {
		return
	}
	parts := make([]string, 0, len(vals)+2)
	parts = append(parts, "RPUSH", qKey)
	for _, v := range vals {
		parts = append(parts, QuoteRedis(v))
	}
	d.writeRestoreCmd(w, parts, ttl)
}

func (d *Dumper) writeRestoreSet(w io.Writer, qKey string, vals []string, ttl time.Duration) {
	if len(vals) == 0 {
		return
	}
	parts := make([]string, 0, len(vals)+2)
	parts = append(parts, "SADD", qKey)
	for _, v := range vals {
		parts = append(parts, QuoteRedis(v))
	}
	d.writeRestoreCmd(w, parts, ttl)
}

func (d *Dumper) writeRestoreZSet(w io.Writer, qKey string, entries []redis.Z, ttl time.Duration) {
	if len(entries) == 0 {
		return
	}
	parts := make([]string, 0, len(entries)*2+2)
	parts = append(parts, "ZADD", qKey)
	for _, z := range entries {
		member, _ := z.Member.(string)
		if member == "" {
			member = fmt.Sprintf("%v", z.Member)
		}
		parts = append(parts, strconvFormatFloat(z.Score), QuoteRedis(member))
	}
	d.writeRestoreCmd(w, parts, ttl)
}
func (d *Dumper) writeWithTTL(w io.Writer, qKey, val string, ttl time.Duration) {
	qVal := QuoteRedis(val)
	switch ttlSec := int64(ttl.Seconds()); {
	case ttlSec > 0:
		fmt.Fprintf(w, "SET %s %s EX %d\n", qKey, qVal, ttlSec)
	case ttl > 0:
		fmt.Fprintf(w, "SET %s %s PXAT %d\n", qKey, qVal, time.Now().Add(ttl).UnixMilli())
	default:
		fmt.Fprintf(w, "SET %s %s\n", qKey, qVal)
	}
}

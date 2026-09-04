// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

type Dumper struct {
	host       string
	port       int
	pass       string
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

	fmt.Fprintf(w, "# dbbackup Redis dump\n")
	fmt.Fprintf(w, "# Host: %s:%d\n#\n\n", d.host, d.port)
	var cursor uint64
	for {
		keys, next, err := d.client.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		for _, key := range keys {
			if d.Tables != nil {
				included, _ := d.Tables.Apply(key)
				if !included {
					continue
				}
			}
			if err := d.dumpKey(ctx, w, key); err != nil {
				continue
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
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

func (d *Dumper) Open() error {
	return d.OpenContext(context.Background())
}

func (d *Dumper) OpenContext(ctx context.Context) error {
	d.ctx = ctx
	probe := func() error { return common.TCPDial(d.host, d.port) }
	connect := func() error {
		opts := &redis.Options{
			Addr:        net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port)),
			Password:    d.pass,
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
		return nil
	}
	return common.WithConnectivity(ctx, "redis", d.connCfg, probe, connect, ping)
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
	typ, err := d.client.Type(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("type: %w", err)
	}
	qKey := QuoteRedis(key)
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
	case "stream", "":

		return nil
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
func strconvFormatFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%v", f), "0"), ".")
}
func (d *Dumper) writeRestoreCmd(w io.Writer, parts []string, ttl time.Duration) {
	fmt.Fprintln(w, strings.Join(parts, " "))
	ttlSec := int64(ttl.Seconds())
	switch {
	case ttlSec > 0:
		fmt.Fprintf(w, "EXPIRE %s %d\n", parts[1], ttlSec)
	case ttlSec < 0:

	default:
		fmt.Fprintf(w, "PEXPIREAT %s %d\n", parts[1], time.Now().Add(ttl).UnixMilli())
	}
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
	ttlSec := int64(ttl.Seconds())
	switch {
	case ttlSec > 0:
		fmt.Fprintf(w, "SET %s %s EX %d\n", qKey, qVal, ttlSec)
	case ttlSec < 0:
		fmt.Fprintf(w, "SET %s %s\n", qKey, qVal)
	default:
		fmt.Fprintf(w, "SET %s %s PXAT %d\n", qKey, qVal, time.Now().Add(ttl).UnixMilli())
	}
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: LicenseRef-NSLv1

// This file is part of the Supported (Supporter) edition of db-backup and is
// licensed under the Nfrastack Supporter License v1 (NSLv1).
// It is excluded from the Community build. !community.

//go:build !community

package redis

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/engine/redis"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

func CheckSupport(ctx context.Context, host string, port int, pass string, tlsCfg *config.TLSConfig) error {
	rdb := redis.ClientFor(host, port, pass, tlsCfg)
	defer rdb.Close()

	if ctx == nil {
		ctx = context.Background()
	}
	info, err := rdb.Info(ctx, "replication").Result()
	if err != nil {
		return fmt.Errorf("replication info: %w", err)
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "master_replid:") {
			return nil
		}
	}
	return fmt.Errorf("could not parse replication info (master_replid missing)")
}

func IncrementalDump(ctx context.Context, w io.Writer, host string, port int, pass string, tlsCfg *config.TLSConfig) error {
	rdb := redis.ClientFor(host, port, pass, tlsCfg)
	defer rdb.Close()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	fmt.Fprintf(w, "# Redis dump\n")
	fmt.Fprintf(w, "# Host: %s:%d\n#\n\n", host, port)

	// SCAN with cursor instead of KEYS * (which blocks Redis).
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		for _, key := range keys {
			ttl, _ := rdb.TTL(ctx, key).Result()
			typ, _ := rdb.Type(ctx, key).Result()
			var val string
			switch typ {
			case "string":
				val, _ = rdb.Get(ctx, key).Result()
			case "list":
				vals, _ := rdb.LRange(ctx, key, 0, -1).Result()
				val = strings.Join(vals, ", ")
			case "set":
				vals, _ := rdb.SMembers(ctx, key).Result()
				val = "{" + strings.Join(vals, ", ") + "}"
			case "hash":
				vals, _ := rdb.HGetAll(ctx, key).Result()
				var parts []string
				for k, v := range vals {
					parts = append(parts, fmt.Sprintf("%s: %s", k, v))
				}
				val = "{" + strings.Join(parts, ", ") + "}"
			default:
				val, _ = rdb.Dump(ctx, key).Result()
			}

			ttlSec := int64(ttl.Seconds())
			if ttlSec > 0 {
				fmt.Fprintf(w, "SET %s %s EX %d\n", redis.QuoteRedis(key), redis.QuoteRedis(val), ttlSec)
			} else {
				fmt.Fprintf(w, "SET %s %s\n", redis.QuoteRedis(key), redis.QuoteRedis(val))
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return nil
}

func IncrementalSpec() registry.IncrementalSpec {
	return registry.IncrementalSpec{
		Engine: "redis",
		CheckSupport: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) error {
			return CheckSupport(ctx, host, port, pass, tlsCfg)
		},
		GetPosition: func(ctx context.Context, host string, port int, user, pass, dbName, authSource string, tlsCfg *config.TLSConfig) (string, error) {
			return Position(ctx, host, port, pass, tlsCfg)
		},
		Dump: func(ctx context.Context, w io.Writer, host string, port int, user, pass, dbName, strategy, since, authSource string, tlsCfg *config.TLSConfig) error {
			return IncrementalDump(ctx, w, host, port, pass, tlsCfg)
		},
	}
}
func Position(ctx context.Context, host string, port int, pass string, tlsCfg *config.TLSConfig) (string, error) {
	rdb := redis.ClientFor(host, port, pass, tlsCfg)
	defer rdb.Close()

	if ctx == nil {
		ctx = context.Background()
	}
	info, err := rdb.Info(ctx, "replication").Result()
	if err != nil {
		return "", fmt.Errorf("replication info: %w", err)
	}

	var replID, offset string
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "master_replid:") {
			replID = strings.TrimSpace(strings.TrimPrefix(line, "master_replid:"))
		}
		if strings.HasPrefix(line, "master_repl_offset:") {
			offset = strings.TrimSpace(strings.TrimPrefix(line, "master_repl_offset:"))
		}
	}
	if replID == "" || offset == "" {
		return "", fmt.Errorf("could not parse replication info")
	}
	return fmt.Sprintf("%s:%s", replID, offset), nil
}
func init() {
	registry.RegisterIncremental(IncrementalSpec())
}

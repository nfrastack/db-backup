// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
)

func ClientFor(host string, port int, pass string, tlsCfg *config.TLSConfig) *redis.Client {
	opts := &redis.Options{
		Addr:        fmt.Sprintf("%s:%d", host, port),
		Password:    pass,
		DialTimeout: 10 * time.Second,
	}
	if tc, err := common.BuildTLSConfig(tlsCfg); err == nil && tc != nil {
		opts.TLSConfig = tc
	}
	return redis.NewClient(opts)
}
func Maintain(host string, port int, pass string, cfg *common.MaintenanceCfg) ([]common.OpResult, error) {
	return nil, nil
}

func Restore(r io.Reader, host string, port int, pass string, tlsCfg *config.TLSConfig) error {
	rdb := ClientFor(host, port, pass, tlsCfg)
	defer rdb.Close()
	ctx := context.Background()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read dump: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 3 || parts[0] != "SET" {
			continue
		}
		key := common.UnquoteRedisArg(parts[1])
		val := common.UnquoteRedisArg(parts[2])

		for _, tok := range []string{" EX ", " PX ", " EXAT ", " PXAT ", " KEEPTTL", " NX", " XX"} {
			if idx := strings.Index(val, tok); idx >= 0 {
				val = val[:idx]
			}
		}
		val = common.UnquoteRedisArg(val)

		if err := rdb.Set(ctx, key, val, 0).Err(); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	return nil
}

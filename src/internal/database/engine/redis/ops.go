// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package redis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/log"
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
	restoreStart := time.Now()
	log.Debug("redis", "restore start", "host", host, "port", port)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var restored, skipped int
	var skippedLines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := common.SplitRedisArgs(line)
		if len(parts) == 0 {
			continue
		}
		if err := restoreLine(ctx, rdb, parts); err != nil {
			skipped++
			if len(skippedLines) < 5 {
				skippedLines = append(skippedLines, line)
			}
			log.Warn("redis", "skipping line - restore failed",
				"line", line, "error", err.Error())
			continue
		}
		restored++
		if restored%10000 == 0 {
			log.Trace("redis", "restore progress", "restored", restored)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dump: %w", err)
	}
	log.Debug("redis", "restore done",
		"restored", restored, "skipped", skipped,
		"elapsed", time.Since(restoreStart).Round(time.Millisecond).String())
	if skipped > 0 {
		return fmt.Errorf("redis: restore incomplete - %d lines skipped (%s)",
			skipped, strings.Join(skippedLines, "; "))
	}
	return nil
}

func restoreLine(ctx context.Context, rdb *redis.Client, parts []string) error {
	verb := strings.ToUpper(parts[0])
	args := make([]string, 0, len(parts)-1)
	for _, a := range parts[1:] {
		args = append(args, common.UnquoteRedisArg(a))
	}
	switch verb {
	case "SET":
		if len(args) < 2 {
			return fmt.Errorf("SET needs key and value")
		}
		val, expireAt, err := splitSetOptions(args[1:])
		if err != nil {
			return err
		}
		if expireAt.IsZero() {
			return rdb.Set(ctx, args[0], val, 0).Err()
		}
		return rdb.SetArgs(ctx, args[0], val, redis.SetArgs{ExpireAt: expireAt}).Err()
	case "HSET":
		if len(args) < 3 || len(args)%2 == 0 {
			return fmt.Errorf("HSET needs key and field/value pairs")
		}
		fields := make(map[string]any, (len(args)-1)/2)
		for i := 1; i+1 < len(args); i += 2 {
			fields[args[i]] = args[i+1]
		}
		return rdb.HSet(ctx, args[0], fields).Err()
	case "RPUSH":
		if len(args) < 2 {
			return fmt.Errorf("RPUSH needs key and values")
		}
		vals := make([]any, 0, len(args)-1)
		for _, v := range args[1:] {
			vals = append(vals, v)
		}
		return rdb.RPush(ctx, args[0], vals...).Err()
	case "SADD":
		if len(args) < 2 {
			return fmt.Errorf("SADD needs key and members")
		}
		members := make([]any, 0, len(args)-1)
		for _, v := range args[1:] {
			members = append(members, v)
		}
		return rdb.SAdd(ctx, args[0], members...).Err()
	case "ZADD":
		if len(args) < 3 || len(args)%2 == 0 {
			return fmt.Errorf("ZADD needs key and score/member pairs")
		}
		members := make([]redis.Z, 0, (len(args)-1)/2)
		for i := 1; i+1 < len(args); i += 2 {
			score, err := strconv.ParseFloat(args[i], 64)
			if err != nil {
				return fmt.Errorf("bad zset score %q: %w", args[i], err)
			}
			members = append(members, redis.Z{Score: score, Member: args[i+1]})
		}
		return rdb.ZAdd(ctx, args[0], members...).Err()
	case "EXPIRE":
		if len(args) != 2 {
			return fmt.Errorf("EXPIRE needs key and seconds")
		}
		sec, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("bad expire seconds %q: %w", args[1], err)
		}
		return rdb.ExpireAt(ctx, args[0], time.Now().Add(time.Duration(sec)*time.Second)).Err()
	case "PEXPIREAT":
		if len(args) != 2 {
			return fmt.Errorf("PEXPIREAT needs key and millis")
		}
		ms, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("bad pexpireat millis %q: %w", args[1], err)
		}
		return rdb.PExpireAt(ctx, args[0], time.UnixMilli(ms)).Err()
	default:
		return fmt.Errorf("unsupported command %q", verb)
	}
}

func splitSetOptions(args []string) (string, time.Time, error) {
	if len(args) == 0 {
		return "", time.Time{}, fmt.Errorf("SET needs a value")
	}
	val := args[0]
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch strings.ToUpper(rest[i]) {
		case "EX", "PX", "EXAT", "PXAT":
			if i+1 >= len(rest) {
				return "", time.Time{}, fmt.Errorf("SET option %q missing value", rest[i])
			}
			n, err := strconv.ParseInt(rest[i+1], 10, 64)
			if err != nil {
				return "", time.Time{}, fmt.Errorf("bad SET option value %q: %w", rest[i+1], err)
			}
			switch strings.ToUpper(rest[i]) {
			case "EX":
				return val, time.Now().Add(time.Duration(n) * time.Second), nil
			case "PX":
				return val, time.Now().Add(time.Duration(n) * time.Millisecond), nil
			case "EXAT":
				return val, time.Unix(n, 0), nil
			case "PXAT":
				return val, time.UnixMilli(n), nil
			}
			i++
		default:
			return "", time.Time{}, fmt.Errorf("unsupported SET option %q", rest[i])
		}
	}
	return val, time.Time{}, nil
}

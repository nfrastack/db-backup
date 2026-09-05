// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
)

func FilePerms(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("sqlite: stat %s: %w", path, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("sqlite: %s is a directory, not a database file", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("sqlite: %s is not readable: %w", path, err)
	}
	f.Close()
	log.Debug("connectivity", "sqlite file readable", "path", path, "size", fi.Size(), "status", "debug")
	return nil
}

func MethodOf(cfg *config.ConnectivityConfig) string {
	if cfg == nil {
		return config.MethodFull
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Method)) {
	case config.MethodNone, config.MethodSimple, config.MethodFull:
		return strings.ToLower(strings.TrimSpace(cfg.Method))
	default:
		return config.MethodFull
	}
}

func retryLoop(ctx context.Context, name string, interval int, deadline time.Time, fn func() error) error {
	extra := LogFieldsFromContext(ctx)
	attempt := 0
	for {
		attempt++
		errCh := make(chan error, 1)
		go func() { errCh <- fn() }()
		var err error
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: connectivity check cancelled: %w", name, ctx.Err())
		case e := <-errCh:
			err = e
		}
		if err == nil {
			if attempt > 1 {
				log.Info("connectivity", "check ok", append(append([]any{}, extra...), "engine", name, "attempt", attempt, "status", "info")...)
			} else {
				log.Debug("connectivity", "check ok", append(append([]any{}, extra...), "engine", name, "attempt", attempt, "status", "debug")...)
			}
			return nil
		}
		remaining := ""
		if !deadline.IsZero() {
			rem := time.Until(deadline).Truncate(time.Second)
			if rem < 0 {
				rem = 0
			}
			remaining = rem.String()
			if time.Now().After(deadline) {
				log.Error("connectivity", "not reachable after timeout", append(append([]any{}, extra...), "engine", name, "attempt", attempt, "error", err, "timeout", interval*attempt, "status", "error")...)
				return fmt.Errorf("%s not reachable after timeout (%ds, %d attempts): %w", name, int(time.Since(deadline.Add(-time.Duration(interval*attempt)*time.Second)).Seconds()), attempt, err)
			}
		}
		log.Warn("connectivity", "check failed - waiting for database", append(append([]any{}, extra...), "engine", name, "attempt", attempt, "error", err, "retry_in", interval, "remaining", remaining, "status", "warn")...)
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%s: connectivity check cancelled: %w", name, ctx.Err())
		case <-timer.C:
		}
	}
}

func TCPDial(host string, port int) error {
	return TCPDialContext(context.Background(), host, port)
}

func TCPDialContext(ctx context.Context, host string, port int) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp dial %s: %w", addr, err)
	}
	conn.Close()
	return nil
}

func WithConnectivity(ctx context.Context, name string, cfg *config.ConnectivityConfig, probe, connect, ping func() error) error {
	extra := LogFieldsFromContext(ctx)
	if cfg == nil || !cfg.Enabled || MethodOf(cfg) == config.MethodNone {
		if cfg != nil && (!cfg.Enabled || MethodOf(cfg) == config.MethodNone) {
			log.Warn("connectivity", "check disabled - backing up blindly", append(append([]any{}, extra...), "engine", name, "status", "warn")...)
		}
		return connect()
	}

	interval := cfg.RetryInterval
	if interval <= 0 {
		interval = 3
	}
	deadline := time.Time{}
	if cfg.Timeout > 0 {
		deadline = time.Now().Add(time.Duration(cfg.Timeout) * time.Second)
	}

	if err := connect(); err != nil {
		log.Error("connectivity", "connect failed", append(append([]any{}, extra...), "engine", name, "method", MethodOf(cfg), "error", err, "status", "error")...)
		return err
	}

	if MethodOf(cfg) == config.MethodSimple {
		if probe == nil {
			return nil
		}
		if err := retryLoop(ctx, name, interval, deadline, probe); err != nil {
			log.Error("connectivity", "reachability check failed", append(append([]any{}, extra...), "engine", name, "method", "simple", "error", err, "status", "error")...)
			return err
		}
		return nil
	}

	return retryLoop(ctx, name, interval, deadline, ping)
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package database

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database/registry"
)

type Strategy string

const (
	StrategyFull         Strategy = "full"
	StrategyIncremental  Strategy = "incremental"
	StrategyDifferential Strategy = "differential"
)

type BackupOptions struct {
	Type       string
	Host       string
	Port       int
	User       string
	Pass       string
	DB         string
	AuthSource string
	TLS        *config.TLSConfig
	Version    int
	Strategy   Strategy
	Since      string
	Objects    config.MysqlObjects
	HasObjects bool
}

func IncrementalsSupported() bool {
	for _, e := range registry.Engines() {
		if registry.LookupIncremental(e) != nil {
			return true
		}
	}
	return false
}
func RunBackup(ctx context.Context, w io.Writer, opts BackupOptions) error {
	if opts.Strategy == StrategyFull || opts.Strategy == "" {
		return runFullBackup(ctx, w, opts)
	}

	spec := registry.LookupIncremental(opts.Type)
	if spec == nil || spec.Dump == nil {
		return runFullBackup(ctx, w, opts)
	}
	return spec.Dump(ctx, w, opts.Host, opts.Port, opts.User, opts.Pass, opts.DB, string(opts.Strategy), opts.Since, opts.AuthSource, opts.TLS)
}
func StrategyNames() []string {
	names := []string{string(StrategyFull)}
	hasIncr := false
	for _, e := range registry.Engines() {
		if registry.LookupIncremental(e) != nil {
			hasIncr = true
			break
		}
	}
	if hasIncr {
		names = append(names, string(StrategyIncremental), string(StrategyDifferential))
	}
	return names
}

func runFullBackup(ctx context.Context, w io.Writer, opts BackupOptions) error {
	engine, err := New(Options{
		Type:       opts.Type,
		Host:       opts.Host,
		Port:       opts.Port,
		User:       opts.User,
		Pass:       opts.Pass,
		DB:         opts.DB,
		Version:    opts.Version,
		TLS:        opts.TLS,
		AuthSource: opts.AuthSource,
		Objects:    opts.Objects,
		HasObjects: opts.HasObjects,
	})
	if err != nil {
		return fmt.Errorf("full backup: %w", err)
	}
	type ctxOpener interface {
		OpenContext(context.Context) error
	}
	if c, ok := engine.(ctxOpener); ok {
		if err := c.OpenContext(ctx); err != nil {
			return fmt.Errorf("open: %w", err)
		}
	} else {
		if err := engine.Open(); err != nil {
			return fmt.Errorf("open: %w", err)
		}
	}
	defer engine.Close()
	return engine.Dump(w, splitDBs(opts.DB))
}

func splitDBs(dbName string) []string {
	var out []string
	for _, n := range strings.Split(dbName, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

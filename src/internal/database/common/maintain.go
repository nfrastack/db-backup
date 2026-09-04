// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"fmt"
	"time"

	"github.com/nfrastack/db-backup/internal/license"
)

type OpResult struct {
	Operation string
	Status    string // OK, SKIP, ERROR
	Detail    string
	Duration  time.Duration
}

type MaintenanceCfg struct {
	Schedule    string
	Optimize    *bool
	Vacuum      *bool
	Reindex     *bool
	Analyze     *bool
	CheckTables *bool
	Compact     *bool
	MemoryPurge *bool
}

var opsTable = []struct {
	Flag    string
	Name    string
	Engines string
}{
	{"analyze", "ANALYZE", "mysql, mariadb, postgres"},
	{"check_tables", "CHECK TABLE / DBCC", "mysql, mariadb, mssql"},
	{"compact", "compact / reIndex", "mongo"},
	{"memory_purge", "MEMORY PURGE", "redis"},
	{"optimize", "OPTIMIZE TABLE", "mysql, mariadb"},
	{"reindex", "REINDEX", "postgres"},
	{"vacuum", "VACUUM", "postgres"},
}

func Enabled(op string, cfg *MaintenanceCfg) bool {
	if op != "check_tables" && license.AllowMaintenance() != nil {
		return false
	}
	switch op {
	case "analyze":
		if cfg.Analyze != nil {
			return *cfg.Analyze
		}
		return true
	case "check_tables":
		if cfg.CheckTables != nil {
			return *cfg.CheckTables
		}
		return true
	case "compact":
		if cfg.Compact != nil {
			return *cfg.Compact
		}
		return true
	case "memory_purge":
		if cfg.MemoryPurge != nil {
			return *cfg.MemoryPurge
		}
		return true
	case "reindex":
		if cfg.Reindex != nil {
			return *cfg.Reindex
		}
		return true
	case "optimize":
		if cfg.Optimize != nil {
			return *cfg.Optimize
		}
		return true
	case "vacuum":
		if cfg.Vacuum != nil {
			return *cfg.Vacuum
		}
		return true
	default:
		return false
	}
}
func FinishOp(r *OpResult, start *time.Time) OpResult {
	r.Duration = time.Since(*start)
	return *r
}

func ListOps() {
	fmt.Println("Available maintenance operations:")
	fmt.Println()
	for _, o := range opsTable {
		fmt.Printf("  --%-14s %-22s %s\n", o.Flag, o.Name, o.Engines)
	}
}
func StartOp(op string) (OpResult, *time.Time) {
	start := time.Now()
	return OpResult{Operation: op, Status: "OK"}, &start
}

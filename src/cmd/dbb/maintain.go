// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
)

func cmdMaintain(args []string) int {
	fs := flag.NewFlagSet("maintain", flag.ExitOnError)
	dbType := fs.String("type", "", "Database type")
	dbHost := fs.String("host", "localhost", "Database host")
	dbPort := fs.Int("port", 0, "Database port")
	dbUser := fs.String("user", "", "Database user")
	dbPass := fs.String("pass", "", "Database password (or file:///path or env://VAR)")
	dbName := fs.String("name", "", "Database name")
	authSource := fs.String("auth-source", "", "Authentication/connect database (Mongo authSource - postgres connect DB for ALL/globals)")
	listOps := fs.Bool("list-ops", false, "List available maintenance operations")
	optimize := fs.Bool("optimize", false, "Enable OPTIMIZE TABLE (MySQL)")
	vacuum := fs.Bool("vacuum", false, "Enable VACUUM (PostgreSQL)")
	reindex := fs.Bool("reindex", false, "Enable REINDEX (PostgreSQL)")
	analyze := fs.Bool("analyze", false, "Enable ANALYZE (MySQL/PostgreSQL)")
	checkTables := fs.Bool("check-tables", false, "Enable CHECK TABLE (MySQL) / DBCC (MSSQL)")
	compact := fs.Bool("compact", false, "Enable compact (MongoDB)")
	memPurge := fs.Bool("memory-purge", false, "Enable MEMORY PURGE (Redis)")
	fs.Parse(args)

	if *listOps {
		database.ListOps()
		return 0
	}

	pass := runner.ResolveSecret(*dbPass)

	var maintainTLS *config.TLSConfig

	if len(fs.Args()) > 0 && *dbType == "" && len(globalConfigPaths) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: job '%s' requires a config file\n", fs.Arg(0))
		fmt.Fprintf(os.Stderr, "  use -c <config.yml> maintain <jobname>\n")
		fmt.Fprintf(os.Stderr, "  or run flat mode (no config):\n")
		fmt.Fprintf(os.Stderr, "    dbb maintain --type <dbtype> --host <host> --user <user> --pass <pass> --name <db> [ops...]\n")
		return 1
	}

	if *dbType == "" && len(globalConfigPaths) > 0 {
		cfg, err := config.LoadConfig(globalConfigPaths...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: config: %v\n", err)
			return 1
		}
		if len(fs.Args()) > 0 {
			jobName := fs.Arg(0)
			for _, job := range cfg.Jobs {
				if job.Name == jobName {
					port := job.Port
					if port == 0 {
						port = runner.DefaultPort(job.Type)
					}
					*dbType = job.Type
					*dbHost = job.Host
					*dbPort = port
					*dbUser = job.User
					if pass == "" {
						pass = runner.ResolveSecret(job.Pass)
					}
					dbNames := ""
					if job.Databases != nil && len(job.Databases.Include) > 0 {
						dbNames = strings.Join(job.Databases.Include, ",")
					}
					*dbName = dbNames
					maintainTLS = job.TLS
					break
				}
			}
			if *dbType == "" {
				fmt.Fprintf(os.Stderr, "ERROR: job '%s' not found in config\n", jobName)
				return 1
			}
		}
	}

	if *dbType == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --type is required (%s)\n", database.TypeList())
		fmt.Fprintf(os.Stderr, "  run: dbb maintain --type <dbtype> --host <host> --user <user> --pass <pass> --name <db> [ops...]\n")
		fmt.Fprintf(os.Stderr, "  or:  dbb -c <config.yml> maintain <jobname>\n")
		return 1
	}

	if *dbPort == 0 {
		*dbPort = runner.DefaultPort(*dbType)
	}

	mcfg := &database.MaintenanceCfg{
		Optimize:    optBool(optimize),
		Vacuum:      optBool(vacuum),
		Reindex:     optBool(reindex),
		Analyze:     optBool(analyze),
		CheckTables: optBool(checkTables),
		Compact:     optBool(compact),
		MemoryPurge: optBool(memPurge),
	}

	results, err := database.Maintain(*dbType, *dbHost, *dbPort, *dbUser, pass, *dbName, *authSource, mcfg, maintainTLS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: maintain: %v\n", err)
		return 1
	}

	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "No maintenance operations ran (all disabled?)\n")
		return 0
	}

	for _, r := range results {
		icon := "✓"
		if r.Status != "OK" {
			icon = "✗"
			if r.Status == "SKIP" {
				icon = "–"
			}
		}
		fmt.Fprintf(os.Stdout, "%s %-20s %s\n", icon, r.Operation, r.Detail)
	}
	return 0
}

func optBool(v *bool) *bool {
	if v != nil && *v {
		return v
	}
	return nil
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
)

func cmdDump(args []string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fs := flag.NewFlagSet("dump", flag.ExitOnError)
	dbType := fs.String("type", "", "Database type ("+database.TypeList()+")")
	dbHost := fs.String("host", "localhost", "Database host")
	dbPort := fs.Int("port", 0, "Database port")
	dbUser := fs.String("user", "", "Database user")
	dbPass := fs.String("pass", "", "Database password (or file:///path or env://VAR)")
	dbName := fs.String("name", "", "Database name(s), comma separated, or ALL")
	dbVersion := fs.Int("version", 0, "Engine version (influx: 1|2; 0 = auto detect)")
	authSource := fs.String("auth-source", "", "Authentication/connect database (mongo authSource, postgres connect DB for ALL/globals)")
	compressType := fs.String("compress", "zstd", "Compression (none|gz|bz|xz|zstd)")
	compressLevel := fs.Int("level", 3, "Compression level")
	checksumType := fs.String("checksum", "md5", "Checksum (md5|sha1|none)")
	storagePath := fs.String("storage-path", config.StoragePath(), "Storage path/prefix (filesystem)")
	storageProfile := fs.String("storage-profile", "", "Storage profile (resolved from -c <config>)")
	strategy := fs.String("strategy", "full", "Backup strategy (full|incremental|differential)")
	splitDB := fs.Bool("split-db", false, "Backup each database into its own file")
	globalsOnly := fs.Bool("globals", false, "Backup only global objects (PostgreSQL roles, grants)")
	tablesInclude := fs.String("tables-include", "", "Only these tables/collections (comma-separated, glob supported)")
	tablesExclude := fs.String("tables-exclude", "", "Skip these tables/collections (comma-separated, glob supported)")
	tablesSchemaOnly := fs.String("tables-schema-only", "", "Dump structure only for these tables (comma separated, * = all)")
	schemaOnly := fs.Bool("schema-only", false, "Dump structure only, no data")
	encryptionType := fs.String("encryption", "", "Encryption type (age|pgp|openpgp|gpg|openssl)")
	ageRecipient := fs.String("age-recipient", "", "Age recipient (public key, comma separated for multiple)")
	agePass := fs.String("age-passphrase", "", "Age passphrase (symmetric encryption)")
	gpgRecipient := fs.String("gpg-recipient", "", "GPG recipient (public key file path or armored key, comma separated for multiple)")
	gpgPass := fs.String("gpg-passphrase", "", "OpenPGP/GPG passphrase (symmetric encryption)")
	opensslPass := fs.String("openssl-passphrase", "", "OpenSSL passphrase (AES-256-CBC pbkdf2)")
	fs.Parse(args)

	if len(fs.Args()) > 0 && *dbType == "" && len(globalConfigPaths) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: job '%s' requires a config file\n", fs.Arg(0))
		fmt.Fprintf(os.Stderr, "  use -c <config.yml> dump <jobname>\n")
		fmt.Fprintf(os.Stderr, "  or run flat mode:\n")
		fmt.Fprintf(os.Stderr, "    dbb dump --type <dbtype> --host <host> --user <user> --pass <pass> --name <db> [--storage-path <dir>]\n")
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
				if job.Name != jobName {
					continue
				}
				if globalProgress != nil {
					job.Progress = globalProgress
				}
				if err := runner.Run(ctx, job, "manual"); err != nil {
					return 1
				}
				return 0
			}
			fmt.Fprintf(os.Stderr, "ERROR: job '%s' not found in config\n", jobName)
			fmt.Fprintf(os.Stderr, "Available jobs:\n")
			for _, j := range cfg.Jobs {
				fmt.Fprintf(os.Stderr, "  %s (type: %s, host: %s)\n", j.Name, j.Type, j.Host)
			}
			return 1
		}

		fmt.Fprintf(os.Stderr, "ERROR: no job name specified\n")
		fmt.Fprintf(os.Stderr, "Usage: dbb -config %s dump <jobname>\n", globalConfigPath)
		fmt.Fprintf(os.Stderr, "Available jobs:\n")
		for _, j := range cfg.Jobs {
			fmt.Fprintf(os.Stderr, "  %s (type: %s, host: %s)\n", j.Name, j.Type, j.Host)
		}
		return 1
	}

	if *dbType == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --type is required (%s)\n", database.TypeList())
		fmt.Fprintf(os.Stderr, "  run: dbb dump --type <dbtype> --host <host> --user <user> --pass <pass> --name <db> [--storage-path <dir>]\n")
		fmt.Fprintf(os.Stderr, "  or:  dbb -c <config.yml> dump <jobname>\n")
		return 1
	}

	dbNames := []string{*dbName}
	if *dbName == "ALL" {
		dbNames = []string{"ALL"}
	}

	storageCfg, err := config.ResolveStorageArg(globalConfigPaths, *storageProfile, *storagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}

	job := config.JobConfig{
		Name:           "",
		Type:           *dbType,
		Host:           *dbHost,
		Port:           *dbPort,
		User:           *dbUser,
		Pass:           runner.ResolveSecret(*dbPass),
		Version:        *dbVersion,
		Checksum:       *checksumType,
		Compression:    &config.CompressionConfig{Type: *compressType, Level: *compressLevel},
		Storage:        storageCfg,
		EncryptionType: *encryptionType,
		Progress:       globalProgress,
		AuthSource:     *authSource,
	}
	if *ageRecipient != "" {
		job.EncryptionIdentities = strings.Split(*ageRecipient, ",")
	}
	if *agePass != "" {
		job.EncryptionIdentity = *agePass
		job.EncryptionIdentities = []string{*agePass}
	}
	if *gpgRecipient != "" {
		job.EncryptionIdentities = strings.Split(*gpgRecipient, ",")
	}
	if *gpgPass != "" {
		job.EncryptionIdentity = *gpgPass
	}
	if *opensslPass != "" {
		job.EncryptionIdentity = *opensslPass
	}
	if job.Backup == nil {
		job.Backup = &config.BackupConfig{}
	}
	job.Backup.Strategy = *strategy
	job.Backup.SchemaOnly = *schemaOnly
	job.SplitDB = *splitDB
	if *globalsOnly {
		job.Databases = &config.DatabaseList{Include: []string{"__globals__"}}
	}
	if len(dbNames) > 0 && *dbName != "" {
		include := dbNames
		if *globalsOnly {
			// docs: include may mix __globals__ with real databases
			include = append(append([]string{}, dbNames...), "__globals__")
		}
		job.Databases = &config.DatabaseList{Include: include}
	}
	if *tablesInclude != "" || *tablesExclude != "" || *tablesSchemaOnly != "" {
		if job.Databases == nil {
			job.Databases = &config.DatabaseList{}
		}
		tf := &config.TableFilter{}
		if *tablesInclude != "" {
			tf.Include = runner.SplitCsv(*tablesInclude)
		}
		if *tablesExclude != "" {
			tf.Exclude = runner.SplitCsv(*tablesExclude)
		}
		if *tablesSchemaOnly != "" {
			if *tablesSchemaOnly == "*" {
				tf.SchemaOnly.All = true
			} else {
				tf.SchemaOnly.Tables = runner.SplitCsv(*tablesSchemaOnly)
			}
		}
		job.Databases.Tables = tf
	}
	if err := runner.Run(ctx, job, "manual"); err != nil {
		return 1
	}
	return 0
}

// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
	"github.com/nfrastack/db-backup/internal/storage"
	"golang.org/x/term"
)

type option struct {
	key       string
	label     string
	value     string
	isDefault bool
}

type countingReader struct {
	r io.Reader
	n atomic.Int64
}

type dbEnvCreds struct {
	Label string
	Type  string
	Host  string
	Port  string
	User  string
	Pass  string
	Name  string
}

func (c *countingReader) Bytes() int64 { return c.n.Load() }
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
func cmdRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	dbType := fs.String("type", "", "Database type ("+database.TypeList()+")")
	dbHost := fs.String("host", "localhost", "Database host")
	dbPort := fs.Int("port", 0, "Database port")
	dbUser := fs.String("user", "", "Database user")
	dbPass := fs.String("pass", "", "Database password (or file:///path or env://VAR)")
	dbName := fs.String("name", "", "Database name(s), comma-separated, or ALL")
	authSource := fs.String("auth-source", "", "Authentication/connect database (mongo authSource, postgres connect DB for ALL/globals)")
	filePath := fs.String("file", "", "Backup file to restore (omit to pick via interactive mode)")
	basePath := fs.String("base", "", "Base full backup (for incremental/differential restore)")
	storagePath := fs.String("storage-path", config.StoragePath(), "Storage path/prefix (filesystem)")
	storageProfile := fs.String("storage-profile", "", "Storage profile (resolved from -c <config>)")
	restoreProfile := fs.String("profile", "", "Restore profile (resolved from -c <config>, profiles.restore)")
	compressType := fs.String("compress", "", "Compression type (auto-detect from filename if empty)")
	encryptionType := fs.String("encryption", "auto", "Encryption type (auto|age|gpg|openssl) - auto detects from magic bytes or the sidecar")
	agePass := fs.String("age-passphrase", "", "Age passphrase for decryption")
	gpgPass := fs.String("gpg-passphrase", "", "OpenPGP/GPG passphrase for decryption")
	identity := fs.String("identity", "", "Age/OpenPGP identity file path or key text for decryption")
	opensslPass := fs.String("openssl-passphrase", "", "OpenSSL passphrase for decryption")
	dryRun := fs.Bool("dry-run", false, "Print what would be restored without doing it")
	nonInteractive := fs.Bool("non-interactive", false, "Never prompt or enter interactive mode")
	fs.Parse(args)

	explicitFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicitFlags[f.Name] = true })

	pass := runner.ResolveSecret(*dbPass)

	var restoreTLS *config.TLSConfig
	var restoreStorage *config.StorageConfig
	var restoreIdentity, restorePassphrase, restoreAuthSource string
	restoreAuthSource = *authSource

	if len(fs.Args()) > 0 && len(globalConfigPaths) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: job '%s' requires a config file\n", fs.Arg(0))
		fmt.Fprintf(os.Stderr, "  use -c <config.yml> restore <jobname>\n")
		fmt.Fprintf(os.Stderr, "  or run flat mode (no config):\n")
		fmt.Fprintf(os.Stderr, "    dbb restore --file <backup> --type <dbtype> --host <host> --user <user> --pass <pass> --name <db>\n")
		return 1
	}

	if len(globalConfigPaths) > 0 {
		cfg, err := config.LoadConfig(globalConfigPaths...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: config: %v\n", err)
			return 1
		}

		r := cfg.EffectiveRestore(*restoreProfile)
		if *restoreProfile != "" && r == nil {
			fmt.Fprintf(os.Stderr, "ERROR: restore profile '%s' not found in config\n", *restoreProfile)
			return 1
		}
		if r != nil {
			if err := license.AllowRestore(); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				fmt.Fprintf(os.Stderr, "  Community restores support AGE encryption and filesystem/S3/webDAV storage only\n")
				fmt.Fprintf(os.Stderr, "  Pass --type/--host/--name/--file on the command line to skip the restore profile\n")
				return 1
			}
		}
		if r != nil {
			if !explicitFlags["file"] && *filePath == "" {
				*filePath = r.File
			}
			if !explicitFlags["base"] && *basePath == "" {
				*basePath = r.Base
			}
			if !explicitFlags["type"] && *dbType == "" {
				*dbType = r.Type
			}
			if !explicitFlags["host"] && *dbHost == "" {
				*dbHost = r.Host
			}
			if !explicitFlags["port"] && *dbPort == 0 {
				*dbPort = r.Port
			}
			if !explicitFlags["user"] && *dbUser == "" {
				*dbUser = r.User
			}
			if !explicitFlags["pass"] && pass == "" {
				pass = r.Pass
			}
			if r.TLS != nil {
				restoreTLS = r.TLS
			}
			if r.Storage != nil {
				restoreStorage = r.Storage
				*storagePath = r.Storage.Path
			}
			restoreIdentity = r.Identity
			restorePassphrase = r.Passphrase
			if !explicitFlags["auth-source"] && restoreAuthSource == "" {
				restoreAuthSource = r.AuthSource
			}
		} else if len(fs.Args()) > 0 {
			jobName := fs.Arg(0)
			for _, job := range cfg.Jobs {
				if job.Name == jobName {
					port := job.Port
					if port == 0 {
						port = runner.DefaultPort(job.Type)
					}
					dbNames := ""
					if job.Databases != nil && len(job.Databases.Include) > 0 {
						dbNames = strings.Join(job.Databases.Include, ",")
					}
					*dbType = job.Type
					*dbHost = job.Host
					*dbPort = port
					*dbUser = job.User
					*dbName = dbNames
					*storagePath = job.Storage.Path
					if pass == "" {
						pass = runner.ResolveSecret(job.Pass)
					}
					restoreTLS = job.TLS
					restoreStorage = job.Storage
					restoreAuthSource = job.AuthSource
					break
				}
			}
			if *dbType == "" {
				fmt.Fprintf(os.Stderr, "ERROR: job '%s' not found in config\n", jobName)
				return 1
			}
		}
	}

	if *storageProfile != "" {
		sc, err := config.ResolveStorageArg(globalConfigPaths, *storageProfile, *storagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		restoreStorage = sc
		*storagePath = sc.Path
	}

	effIdentity, effGPGPass, effOpenSSLPass := *identity, *gpgPass, *opensslPass
	if restoreIdentity != "" && effIdentity == "" {
		effIdentity = restoreIdentity
	}
	if restorePassphrase != "" {
		if effGPGPass == "" {
			effGPGPass = restorePassphrase
		}
		if effOpenSSLPass == "" {
			effOpenSSLPass = restorePassphrase
		}
	}

	if _, err := database.BuildTLSConfig(restoreTLS); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: tls: %v\n", err)
		return 1
	}

	restoreCfg := restoreStorage
	if restoreCfg == nil {
		var err error
		restoreCfg, err = config.ResolveStorageArg(globalConfigPaths, *storageProfile, *storagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
	}

	switch strings.ToLower(restoreCfg.Backend) {
	case "azure", "gcs":
		if err := license.AllowStorage(); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: storage backend %q requires Supporter license (%s)\n", restoreCfg.Backend, err)
			fmt.Fprintf(os.Stderr, "  Community restores support filesystem, S3 and webDAV storage only\n")

			return 1
		}
	}
	st, err := storage.New(storage.Backend(restoreCfg.Backend), runner.StorageOpts(restoreCfg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", err)
		return 1
	}

	if runner.IsGlobPattern(*filePath) {
		resolved, err := runner.ResolveGlobFile(st, *filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			return 1
		}
		log.Debug("restore", "resolved glob to", "file", resolved, "pattern", *filePath)
		*filePath = resolved
	}

	if *filePath != "" {
		if sc, scErr := retention.ReadSidecar(st, *filePath); scErr == nil && sc != nil {
			if !explicitFlags["type"] && *dbType == "" && sc.Type != "" {
				*dbType = sc.Type
			}
			if !explicitFlags["name"] && *dbName == "" && sc.DB != "" {
				*dbName = sc.DB
			}
			if !explicitFlags["host"] && *dbHost == "" && sc.Host != "" {
				*dbHost = sc.Host
			}
		}
		if !explicitFlags["type"] && *dbType == "" {
			if bi, err := database.ParseBackupFilename(*filePath); err == nil && bi.Type != "" {
				*dbType = bi.Type
			}
		}
		if !explicitFlags["name"] && *dbName == "" {
			if bi, err := database.ParseBackupFilename(*filePath); err == nil && bi.DBName != "" {
				*dbName = bi.DBName
			}
		}
		if !explicitFlags["host"] && *dbHost == "" {
			if bi, err := database.ParseBackupFilename(*filePath); err == nil {
				*dbHost = bi.Host
			}
		}
	}

	configMode := len(globalConfigPaths) > 0

	if *filePath == "" && *dbType == "" && len(fs.Args()) == 0 {
		if *nonInteractive {
			fmt.Fprintf(os.Stderr, "ERROR: restore requires a backup file (--file). run without --non-interactive to enter interactive mode\n")
			return 1
		}
		interactiveRestore()
		return 0
	}

	if *filePath == "" {
		if !*nonInteractive && !configMode && isTerminal(os.Stdin) {
			fmt.Fprintf(os.Stderr, "Missing --file (which backup to restore).\n")
			fmt.Fprintf(os.Stderr, "Enter interactive mode to pick one? [y/N]: ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			line = strings.TrimSpace(line)
			if strings.EqualFold(line, "y") || strings.EqualFold(line, "yes") {
				interactiveRestore()
				return 0
			}
		}
		fmt.Fprintf(os.Stderr, "ERROR: --file is required (or run with no args for interactive mode)\n")
		return 1
	}

	if *dbType == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --type is required (mysql|postgres|mongo|mssql|redis|sqlite)\n")
		fmt.Fprintf(os.Stderr, "  run: dbb restore --file <backup> --type <dbtype> --host <host> --user <user> --pass <pass> --name <db>\n")
		fmt.Fprintf(os.Stderr, "  or:  dbb -c <config.yml> restore <jobname>\n")
		return 1
	}

	if *dbPort == 0 {
		*dbPort = runner.DefaultPort(*dbType)
	}

	if *dryRun {
		fmt.Fprintf(os.Stderr, "[DRY-RUN] Would restore %s to %s/%s:%d (db: %s)\n",
			*filePath, *dbHost, *dbType, *dbPort, *dbName)
		if *basePath != "" {
			fmt.Fprintf(os.Stderr, "[DRY-RUN]   base: %s\n", *basePath)
		}
		return 0
	}

	order := []string{*filePath}
	{
		cur := *filePath
		for i := 0; i < 64; i++ {
			base := ""
			if cur == *filePath && *basePath != "" {
				base = *basePath
			} else if sc, err := retention.ReadSidecar(st, cur); err == nil {
				base = sc.Base
			}
			base = strings.TrimSuffix(base, ".json")
			if base == "" || base == cur {
				break
			}
			order = append([]string{base}, order...)
			cur = base
		}
	}
	if len(order) > 1 {
		log.Debug("restore", "chain resolved", "depth", len(order), "order", strings.Join(order, " -> "))
	}

	opts := retention.DecryptOpts{
		EncryptionType: *encryptionType,
		AgePass:        *agePass,
		GPGPass:        effGPGPass,
		OpenSSLPAss:    effOpenSSLPass,
		Identity:       effIdentity,
		FallbackPass:   restorePassphrase,
		FallbackID:     restoreIdentity,
	}

	for gi, gf := range order {
		isMain := gi == len(order)-1

		log.Debug("restore", "downloading", "file", gf)
		rc, _, err := st.Download(context.Background(), gf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: download %s: %v\n", gf, err)
			return 1
		}

		var r io.Reader = rc

		var encMeta *retention.EncryptionMeta
		if sidecar, serr := retention.ReadSidecar(st, gf); serr == nil {
			encMeta = sidecar.Encryption
		}
		if encMeta == nil {
			ext := filepath.Ext(gf)
			if ext == ".age" || ext == ".gpg" || ext == ".enc" {
				encMeta = &retention.EncryptionMeta{Type: strings.TrimPrefix(ext, ".")}
			}
		}
		if isMain {
			if effGPGPass != "" && encMeta == nil {
				encMeta = &retention.EncryptionMeta{Type: "gpg"}
			} else if effOpenSSLPass != "" && encMeta == nil {
				encMeta = &retention.EncryptionMeta{Type: "openssl"}
			}
		} else if encMeta == nil {
			if effGPGPass != "" {
				encMeta = &retention.EncryptionMeta{Type: "gpg"}
			} else if effOpenSSLPass != "" {
				encMeta = &retention.EncryptionMeta{Type: "openssl"}
			}
		}

		compressHint := *compressType
		if !isMain {
			compressHint = ""
		}

		decoded, oerr := retention.OpenBackup(r, encMeta, opts, gf, compressHint)
		if oerr != nil {
			rc.Close()
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", oerr)
			if encMeta != nil {
				switch encMeta.Type {
				case "age":
					fmt.Fprintf(os.Stderr, "  Provide --identity, --age-passphrase, or set them in the restore config\n")
				case "gpg", "openpgp", "pgp":
					fmt.Fprintf(os.Stderr, "  Provide --identity, --gpg-passphrase, or set them in the restore config\n")
				case "openssl", "aes-256-cbc", "aes256":
					fmt.Fprintf(os.Stderr, "  Provide --openssl-passphrase or set passphrase in the restore config\n")
				}
			}
			return 1
		}

		stage := "Restoring"
		if !isMain {
			stage = fmt.Sprintf("Restoring ancestor (%d/%d)", gi+1, len(order)-1)
		}
		fmt.Fprintf(os.Stderr, "%s %s -> %s/%s:%d (db: %s)\n",
			stage, gf, *dbHost, *dbType, *dbPort, *dbName)

		cr := &countingReader{r: decoded}
		stop := startMeter(stage+" "+formatBytes(cr.Bytes())+": ", &cr.n)

		if err := database.RestoreTo(cr, *dbType, *dbHost, *dbPort, *dbUser, pass, *dbName, restoreAuthSource, restoreTLS); err != nil {
			close(stop)
			rc.Close()
			fmt.Fprintf(os.Stderr, "ERROR: restore %s: %v\n", gf, err)
			return 1
		}
		close(stop)
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(os.Stderr, "%s: %s streamed\033[K\n", stage, formatBytes(cr.Bytes()))
		rc.Close()
	}

	fmt.Fprintf(os.Stderr, "Restore complete (%d backup(s))\n", len(order))
	return 0
}

func detectDBEnvCreds() []dbEnvCreds {
	re := regexp.MustCompile(`^DB(\d*)_(TYPE|HOST|PORT|USER|PASS|NAME)$`)
	byIdx := map[string]map[string]string{}
	var order []string
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		m := re.FindStringSubmatch(parts[0])
		if m == nil {
			continue
		}
		idx := m[1]
		if idx == "" {
			idx = "00"
		}
		key := "DB" + idx
		if _, ok := byIdx[key]; !ok {
			byIdx[key] = map[string]string{}
			order = append(order, key)
		}
		byIdx[key][m[2]] = parts[1]
	}
	sort.Strings(order)
	out := make([]dbEnvCreds, 0, len(order))
	for _, key := range order {
		v := byIdx[key]
		if v["HOST"] == "" || v["USER"] == "" {
			continue
		}
		c := dbEnvCreds{
			Label: key,
			Type:  strings.ToLower(v["TYPE"]),
			Host:  v["HOST"],
			Port:  v["PORT"],
			User:  v["USER"],
			Pass:  v["PASS"],
			Name:  v["NAME"],
		}
		out = append(out, c)
	}
	return out
}
func expandEnvValue(v string) (string, bool) {
	s := strings.TrimSpace(v)
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		return os.Getenv(s[2 : len(s)-1]), true
	}
	if strings.HasPrefix(s, "$") && len(s) > 1 {
		return os.Getenv(s[1:]), true
	}
	return s, false
}
func formatBytes(n int64) string {
	switch {
	case n >= 1024*1024*1024:
		return fmt.Sprintf("%.2fGB", float64(n)/(1024*1024*1024))
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
func interactiveRestore() {
	rd := bufio.NewReader(os.Stdin)

	fmt.Fprintf(os.Stderr, "\ndb-backup Interactive Restore\n")
	fmt.Fprintf(os.Stderr, "=============================\n\n")

	storeBackend := "filesystem"
	storePath := config.StoragePath()
	var cfg *config.Config
	var restoreProfileNames []string
	if len(globalConfigPaths) > 0 {
		cfg, _ = config.LoadConfig(globalConfigPaths...)
		if cfg != nil && cfg.RestoreProfiles != nil {
			for name := range cfg.RestoreProfiles {
				restoreProfileNames = append(restoreProfileNames, name)
			}
			sort.Strings(restoreProfileNames)
		}
	}

	profileMode := false
	var effRestore *config.RestoreConfig
	var profileCfg *config.StorageConfig
	if len(restoreProfileNames) > 0 {
		fmt.Fprintf(os.Stderr, "\nRestore profiles in config:\n")
		for i, name := range restoreProfileNames {
			conn := cfg.RestoreProfiles[name].Connection
			extra := ""
			if conn != "" {
				extra = " (connection: " + conn + ")"
			}
			fmt.Fprintf(os.Stderr, "  %d) '%s'%s\n", i+1, name, extra)
		}
		fmt.Fprintf(os.Stderr, "Use a restore profile? [1-%d, N]: ", len(restoreProfileNames))
		if v := strings.TrimSpace(readLine(rd)); v != "" && strings.ToLower(v) != "n" {
			if idx, err := strconv.Atoi(v); err == nil && idx >= 1 && idx <= len(restoreProfileNames) {
				if r := cfg.EffectiveRestore(restoreProfileNames[idx-1]); r != nil {
					effRestore = r
					profileMode = true
				}
			}
		}
	}

	var storageProfileNames []string
	if cfg != nil && cfg.StorageProfiles != nil {
		for name := range cfg.StorageProfiles {
			storageProfileNames = append(storageProfileNames, name)
		}
		sort.Strings(storageProfileNames)
	}

	if profileMode {
		if effRestore.Storage != nil {
			switch strings.ToLower(effRestore.Storage.Backend) {
			case "azure", "gcs":
				if err := license.AllowStorage(); err != nil {
					fmt.Fprintf(os.Stderr, "ERROR: storage backend %q requires Supporter license (%s)\n", effRestore.Storage.Backend, err)

					os.Exit(1)
				}
			}
		}
		var pst storage.Storage
		var perr error
		if effRestore.Storage != nil {
			pst, perr = storage.New(storage.Backend(effRestore.Storage.Backend), runner.StorageOpts(effRestore.Storage))
		} else {
			fmt.Fprintf(os.Stderr, "\nStorage path [/var/backups]: ")
			line, _ := rd.ReadString('\n')
			sp := strings.TrimSpace(line)
			if sp == "" {
				sp = config.StoragePath()
			}
			pst, perr = storage.New(storage.Backend("filesystem"), map[string]string{"path": sp})
		}
		if perr != nil {
			fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", perr)
			os.Exit(1)
		}

		fileName := effRestore.File
		if fileName == "" {
			fmt.Fprintf(os.Stderr, "ERROR: restore profile does not define file\n")
			os.Exit(1)
		}
		if strings.ContainsAny(fileName, "*?[") {
			resolved, rerr := runner.ResolveGlobFile(pst, fileName)
			if rerr != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", rerr)
				os.Exit(1)
			}
			fileName = resolved
		}
		parsed, _ := database.ParseBackupFilename(fileName)

		portNum := effRestore.Port
		if portNum == 0 {
			portNum = runner.DefaultPort(parsed.Type)
		}

		fmt.Fprintf(os.Stderr, "\nRestore profile '%s':\n", restoreProfileNames[0])
		fmt.Fprintf(os.Stderr, "  File:     %s\n  Type:     %s\n  Host:     %s\n  Port:     %d\n  Database: %s\n  User:     %s\n",
			fileName, parsed.Type, effRestore.Host, portNum, parsed.DBName, effRestore.User)

		if sc, err := retention.ReadSidecar(pst, fileName); err == nil {
			printSidecarPanel(sc)
		}

		fmt.Fprintf(os.Stderr, "\nProceed with restore? [Y/n]: ")
		c, _ := rd.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(c)) == "n" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			os.Exit(0)
		}

		order := []string{fileName}
		cur := fileName
		for i := 0; i < 64; i++ {
			base := ""
			if sc, err := retention.ReadSidecar(pst, cur); err == nil {
				base = strings.TrimSuffix(sc.Base, ".json")
			}
			if base == "" || base == cur {
				break
			}
			order = append([]string{base}, order...)
			cur = base
		}

		totalStart := time.Now()
		for gi, gf := range order {
			t0 := time.Now()
			rc, _, err := pst.Download(context.Background(), gf)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: download %s: %v\n", gf, err)
				os.Exit(1)
			}
			cr := &countingReader{r: rc}
			stop := startMeter(fmt.Sprintf("[%d/%d] %s: ", gi+1, len(order), gf), &cr.n)

			var encMeta *retention.EncryptionMeta
			if sc, err := retention.ReadSidecar(pst, gf); err == nil {
				encMeta = sc.Encryption
			}
			opts := retention.DecryptOpts{
				FallbackPass: effRestore.Passphrase,
				FallbackID:   effRestore.Identity,
				Identity:     effRestore.Identity,
			}
			decoded, oerr := retention.OpenBackup(cr, encMeta, opts, gf, "")
			if oerr != nil {
				rc.Close()
				close(stop)
				fmt.Fprintf(os.Stderr, "\nERROR: open %s: %v\n", gf, oerr)
				os.Exit(1)
			}
			memType, memDB := parsed.Type, parsed.DBName
			if memParsed, mperr := database.ParseBackupFilename(gf); mperr == nil {
				memType, memDB = memParsed.Type, memParsed.DBName
			}
			memPort := portNum
			if memPort == 0 {
				memPort = runner.DefaultPort(memType)
			}
			if err := database.RestoreTo(decoded, memType, effRestore.Host, memPort,
				effRestore.User, effRestore.Pass, memDB, "", nil); err != nil {
				rc.Close()
				close(stop)
				fmt.Fprintf(os.Stderr, "\nERROR: restore %s: %v\n", gf, err)
				os.Exit(1)
			}
			rc.Close()
			close(stop)
			fmt.Fprintf(os.Stderr, "[%d/%d] %s restored in %s\033[K\n",
				gi+1, len(order), gf, time.Since(t0).Round(time.Millisecond))
		}
		fmt.Fprintf(os.Stderr, "\nRestore complete (%d backup(s), total %s)\n",
			len(order), time.Since(totalStart).Round(time.Millisecond))
		return
	}
	if len(storageProfileNames) > 0 {
		fmt.Fprintf(os.Stderr, "\nStorage backend:\n  1) filesystem\n")
		for i, name := range storageProfileNames {
			be := cfg.StorageProfiles[name].Backend
			fmt.Fprintf(os.Stderr, "  %d) profile '%s' (%s)\n", i+2, name, be)
		}
		fmt.Fprintf(os.Stderr, "  c) Custom filesystem path\n  q) Quit\n")
		for {
			fmt.Fprintf(os.Stderr, "Select [1]: ")
			line := strings.TrimSpace(readLine(rd))
			if line == "" || line == "1" {
				break
			}
			if strings.ToLower(line) == "q" {
				fmt.Fprintln(os.Stderr, "Bye!")
				os.Exit(0)
			}
			if strings.ToLower(line) == "c" {
				break
			}
			if idx, aerr := strconv.Atoi(line); aerr == nil && idx >= 2 && idx <= len(storageProfileNames)+1 {
				name := storageProfileNames[idx-2]
				sc, lerr := config.LoadStorageProfile(globalConfigPaths, name)
				if lerr != nil {
					fmt.Fprintf(os.Stderr, "ERROR loading profile %s: %v\n", name, lerr)
					continue
				}
				profileCfg = sc
				storeBackend = sc.Backend
				storePath = sc.Path
				break
			}
			fmt.Fprintf(os.Stderr, "Invalid option.\n")
		}
	} else {
		fmt.Fprintf(os.Stderr, "\nStorage backend [filesystem].\n")
		fmt.Fprintf(os.Stderr, "  Other backends (s3 / azure / gcs / webdav) require -c <config.yml> with a storage profile.\n")
	}

	if profileCfg == nil {
		fmt.Fprintf(os.Stderr, "Storage path [/var/backups]: ")
		if line, _ := rd.ReadString('\n'); strings.TrimSpace(line) != "" {
			storePath = strings.TrimSpace(line)
		}
	}

	var st storage.Storage
	var err error
	if profileCfg != nil {
		switch strings.ToLower(profileCfg.Backend) {
		case "azure", "gcs":
			if err := license.AllowStorage(); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: storage backend %q requires Supporter license (%s)\n", profileCfg.Backend, err)

				os.Exit(1)
			}
		}
		st, err = storage.New(storage.Backend(profileCfg.Backend), runner.StorageOpts(profileCfg))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", err)
			os.Exit(1)
		}
	} else {
		if !strings.EqualFold(storeBackend, "filesystem") {
			fmt.Fprintf(os.Stderr, "ERROR: storage backend %q requires --storage-profile (credentials come from a storage profile)\n", storeBackend)
			os.Exit(1)
		}
		st, err = storage.New(storage.Backend(storeBackend), map[string]string{"path": storePath})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", err)
			os.Exit(1)
		}
	}

	entries, err := st.List(context.Background(), "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: list: %v\n", err)
		os.Exit(1)
	}

	var backups []storage.Entry
	for _, e := range entries {
		if !database.IsBackupFile(e.Path) {
			continue
		}
		backups = append(backups, e)
	}

	if len(backups) == 0 {
		fmt.Fprintf(os.Stderr, "No backups found at %s/%s\n", storeBackend, storePath)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\nAvailable backups:\n")
	for i, e := range backups {
		bi, err := database.ParseBackupFilename(e.Path)
		label := e.Path
		size := ""
		if e.Size > 0 {
			size = formatBytes(e.Size)
		}
		if err == nil {
			label = fmt.Sprintf("%s/%s/%s [%s] %s",
				bi.Type, bi.DBName, bi.Host, bi.Strategy,
				bi.Timestamp.Format("2006-01-02 15:04:05"))
			tag := bi.Compress
			if bi.Encryption != "" {
				if tag != "" {
					tag += "+"
				}
				tag += bi.Encryption
			}
			if tag != "" {
				label += "  {" + tag + "}"
			}
		}
		fmt.Fprintf(os.Stderr, "  %2d) %-72s %12s\n", i+1, label, size)
	}
	fmt.Fprintf(os.Stderr, "  %2s) %s\n", "c", "Custom path")
	fmt.Fprintf(os.Stderr, "  %2s) %s\n", "q", "Quit")

	var fileName string
	for {
		fmt.Fprintf(os.Stderr, "\nSelect backup [1-%d, c, q]: ", len(backups))
		line, _ := rd.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "q" || line == "quit" {
			fmt.Fprintln(os.Stderr, "Bye!")
			os.Exit(0)
		}
		if line == "c" || line == "custom" {
			fmt.Fprintf(os.Stderr, "Enter path: ")
			if v, _ := rd.ReadString('\n'); strings.TrimSpace(v) != "" {
				fileName = strings.TrimSpace(v)
				break
			}
			continue
		}
		var idx int
		if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= len(backups) {
			fileName = backups[idx-1].Path
			break
		}
		fmt.Fprintf(os.Stderr, "Invalid option. Try again.\n")
	}

	parsed, _ := database.ParseBackupFilename(fileName)

	var sc *retention.Sidecar
	if s, err := retention.ReadSidecar(st, fileName); err == nil && s != nil {
		sc = s
		printSidecarPanel(sc)
	} else {
		fmt.Fprintf(os.Stderr, "\n(no sidecar metadata available for this file)\n")
	}

	defaultType := ""
	if sc != nil && sc.Type != "" {
		defaultType = sc.Type
	} else if parsed != nil {
		defaultType = parsed.Type
	}
	rType := promptSelect(rd, "Database type", restoreTypeOptions(defaultType), defaultType)

	var envPick *dbEnvCreds
	if envCreds := detectDBEnvCreds(); len(envCreds) > 0 {
		for i := range envCreds {
			if envCreds[i].Type != "" && (strings.HasPrefix(envCreds[i].Type, rType) || strings.HasPrefix(rType, envCreds[i].Type)) {
				envCreds[0], envCreds[i] = envCreds[i], envCreds[0]
				break
			}
		}
		fmt.Fprintf(os.Stderr, "\nDetected database credentials in environment:\n")
		for i, c := range envCreds {
			port := c.Port
			if port == "" {
				port = "-"
			}
			fmt.Fprintf(os.Stderr, "  %d) %s (%s · %s:%s · user=%s)\n", i+1, c.Label, orDash(c.Type), c.Host, port, c.User)
		}
		if len(envCreds) == 1 {
			fmt.Fprintf(os.Stderr, "Use these credentials? [Y/n]: ")
			if v := strings.ToLower(readLine(rd)); v != "n" && v != "no" {
				envPick = &envCreds[0]
			}
		} else {
			fmt.Fprintf(os.Stderr, "Use which? [1-%d, N]: ", len(envCreds))
			if v := readLine(rd); v != "" && strings.ToLower(v) != "n" {
				if idx, err := strconv.Atoi(v); err == nil && idx >= 1 && idx <= len(envCreds) {
					envPick = &envCreds[idx-1]
				}
			}
		}
	}

	var rHost, rUser, rPass string

	defaultDB := ""
	if sc != nil && sc.DB != "" {
		defaultDB = sc.DB
	} else if parsed != nil {
		defaultDB = parsed.DBName
	} else if envPick != nil && envPick.Name != "" {
		defaultDB = envPick.Name
	}
	rDB := promptValue(rd, "Database name", defaultDB, "Parsed from filename", false)

	defaultHost := ""
	if sc != nil && sc.Host != "" {
		defaultHost = sc.Host
	} else if parsed != nil {
		defaultHost = parsed.Host
	}

	rPort := ""
	if envPick != nil {
		rHost = envPick.Host
		rUser = envPick.User
		rPass = promptSecret(rd, fmt.Sprintf("Password for %s (blank to reuse $%s_PASS): ", envPick.User, envPick.Label))
		if rPass == "" {
			rPass = envPick.Pass
		}
		rPort = envPick.Port
	} else {
		rHost = promptValue(rd, "Host", defaultHost, "Parsed from filename", false)
		rUser = promptValue(rd, "User", "", "", false)
		rPass = promptValue(rd, "Password", "", "", true)
		portStr := ""
		if v := runner.DefaultPort(rType); v > 0 {
			portStr = fmt.Sprintf("%d", v)
		}
		rPort = promptValue(rd, "Port", portStr, "Engine default for "+rType, false)
	}
	_ = defaultHost

	rSSL := "false"
	switch rType {
	case "mongo", "mssql", "mysql", "postgres", "redis":
		fmt.Fprintf(os.Stderr, "Use TLS/SSL? [y/N]: ")
		v := readLine(rd)
		v = strings.ToLower(v)
		if v == "y" || v == "yes" {
			rSSL = "true"
		}
	}

	rStrategy := retention.StrategyFromFilename(fileName)
	fmt.Fprintf(os.Stderr, "\nRestore Summary:\n")
	fmt.Fprintf(os.Stderr, "  File:     %s\n", fileName)
	fmt.Fprintf(os.Stderr, "  Strategy: %s\n", rStrategy)
	fmt.Fprintf(os.Stderr, "  Type:     %s\n", rType)
	fmt.Fprintf(os.Stderr, "  Host:     %s\n", rHost)
	fmt.Fprintf(os.Stderr, "  Port:     %s\n", rPort)
	fmt.Fprintf(os.Stderr, "  Database: %s\n", rDB)
	fmt.Fprintf(os.Stderr, "  User:     %s\n", rUser)
	fmt.Fprintf(os.Stderr, "  SSL:      %s\n", rSSL)
	fmt.Fprintf(os.Stderr, "\nProceed with restore? [Y/n]: ")
	confirm, _ := rd.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))
	if confirm == "n" || confirm == "no" {
		fmt.Fprintln(os.Stderr, "Aborted.")
		os.Exit(0)
	}

	st2, err := storage.New(storage.Backend(storeBackend), map[string]string{"path": storePath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: storage: %v\n", err)
		os.Exit(1)
	}

	chain := []string{fileName}
	{
		cur := fileName
		for i := 0; i < 64; i++ {
			s, err := retention.ReadSidecar(st2, cur)
			if err != nil || s.Base == "" {
				break
			}
			if s.Base == cur {
				break
			}
			chain = append([]string{s.Base}, chain...)
			cur = s.Base
		}
	}
	if len(chain) > 1 {
		fmt.Fprintf(os.Stderr, "\nIncremental chain (%d backups, oldest first):\n", len(chain))
		for i, f := range chain {
			mark := ""
			if f == fileName {
				mark = "  <- selected"
			}
			fmt.Fprintf(os.Stderr, "  %d) %s%s\n", i+1, f, mark)
		}
		fmt.Fprintf(os.Stderr, "All chain members will be restored in order. Proceed? [Y/n]: ")
		c, _ := rd.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(c)) == "n" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			os.Exit(0)
		}
	}

	var encMeta *retention.EncryptionMeta
	if sc, err := retention.ReadSidecar(st2, fileName); err == nil && sc.Encryption != nil && sc.Encryption.Type != "" {
		encMeta = sc.Encryption
	} else if ext := filepath.Ext(fileName); ext == ".age" || ext == ".gpg" || ext == ".enc" {
		encMeta = &retention.EncryptionMeta{Type: strings.TrimPrefix(ext, ".")}
	} else if strings.Contains(fileName, ".age.") {
		encMeta = &retention.EncryptionMeta{Type: "age"}
	} else if strings.Contains(fileName, ".enc.") {
		encMeta = &retention.EncryptionMeta{Type: "openssl"}
	} else if strings.Contains(fileName, ".gpg.") {
		encMeta = &retention.EncryptionMeta{Type: "gpg"}
	}

	var agePass, gpgPass, oslPass, identity string
	if encMeta != nil {
		switch encMeta.Type {
		case "age":
			if encMeta.Passphrase {
				agePass = promptSecret(rd, "Age passphrase")
			} else {
				identity = promptValue(rd, "Age identity file (private key)", "", "", false)
			}
		case "gpg":
			gpgPass = promptSecret(rd, "GPG passphrase (empty if using a local keyring)")
		case "openssl":
			oslPass = promptSecret(rd, "OpenSSL passphrase")
		}
		log.Debug("restore", "interactive decryption prepared", "type", encMeta.Type,
			"has_passphrase", agePass != "" || gpgPass != "" || oslPass != "", "has_identity", identity != "")
	}

	totalStart := time.Now()
	portNum, _ := strconv.Atoi(rPort)
	if portNum == 0 {
		portNum = runner.DefaultPort(rType)
	}

	for idx, item := range chain {
		t0 := time.Now()
		rc, size, err := st2.Download(context.Background(), item)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: download %s: %v\n", item, err)
			os.Exit(1)
		}
		cr := &countingReader{r: rc}
		stop := startMeter("download "+formatBytes(cr.Bytes()), &cr.n)
		r := io.Reader(cr)

		opts := retention.DecryptOpts{
			AgePass:      agePass,
			GPGPass:      gpgPass,
			OpenSSLPAss:  oslPass,
			Identity:     identity,
			FallbackPass: "",
		}
		var enc *retention.EncryptionMeta
		if item == fileName {
			enc = encMeta
		} else if s, err := retention.ReadSidecar(st2, item); err == nil {
			enc = s.Encryption
		}
		decoded, derr := retention.OpenBackup(r, enc, opts, item, "")
		if derr != nil {
			rc.Close()
			close(stop)
			fmt.Fprintf(os.Stderr, "\nERROR: open %s: %v\n", item, derr)
			os.Exit(1)
		}
		close(stop)
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(os.Stderr, "\rdownload : %s in %s\033[K\n", formatBytes(size), time.Since(t0).Round(time.Millisecond))

		t1 := time.Now()
		dr := &countingReader{r: decoded}
		stop2 := startMeter("streaming "+formatBytes(dr.Bytes()), &dr.n)

		if idx == len(chain)-1 {
			fmt.Fprintf(os.Stderr, "restoring -> %s/%s on %s:%d...\n", rType, rDB, rHost, portNum)
		} else {
			fmt.Fprintf(os.Stderr, "restoring base (%d/%d)...\n", idx+1, len(chain))
		}
		if err := database.RestoreTo(dr, rType, rHost, portNum, rUser, rPass, rDB, "", nil); err != nil {
			close(stop2)
			fmt.Fprintf(os.Stderr, "\nERROR: restore %s: %v\n", item, err)
			os.Exit(1)
		}
		close(stop2)
		rc.Close()
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintf(os.Stderr, "\rrestored  : %s streamed in %s\033[K\n", formatBytes(dr.Bytes()), time.Since(t1).Round(time.Millisecond))
	}

	fmt.Fprintf(os.Stderr, "\nRestore complete: %d backup(s) -> %s/%s @ %s:%d (total %s)\n",
		len(chain), rType, rDB, rHost, portNum, time.Since(totalStart).Round(time.Millisecond))
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func printSidecarPanel(sc *retention.Sidecar) {
	fmt.Fprintf(os.Stderr, "\nBackup details (sidecar):\n")
	if sc.Job != nil && sc.Job.Name != "" {
		line := sc.Job.Name
		if sc.Job.Schedule != "" {
			line += " (" + sc.Job.Schedule + ")"
		}
		fmt.Fprintf(os.Stderr, "  job      : %s\n", line)
	}
	if sc.Tool != nil {
		tool := sc.Tool.Name
		if tool == "" {
			tool = "db-backup"
		}
		if sc.Tool.Version != "" {
			tool += " " + sc.Tool.Version
		}
		if sc.Tool.Edition != "" {
			tool += " (" + sc.Tool.Edition + ")"
		}
		fmt.Fprintf(os.Stderr, "  tool     : %s\n", tool)
	}
	fmt.Fprintf(os.Stderr, "  trigger  : %s\n", orDash(sc.Trigger))
	fmt.Fprintf(os.Stderr, "  taken    : %s\n", orDash(sc.Timestamp))
	fmt.Fprintf(os.Stderr, "  strategy : %s\n", orDash(sc.Strategy))
	if sc.SchemaOnly {
		fmt.Fprintf(os.Stderr, "  content  : schema only\n")
	}
	if t := sc.Tables; t != nil {
		if len(t.Include) > 0 {
			fmt.Fprintf(os.Stderr, "  tables   : include %s\n", strings.Join(t.Include, ", "))
		}
		if len(t.Exclude) > 0 {
			fmt.Fprintf(os.Stderr, "  tables   : exclude %s\n", strings.Join(t.Exclude, ", "))
		}
		if len(t.SchemaOnly) > 0 {
			fmt.Fprintf(os.Stderr, "  tables   : schema-only %s\n", strings.Join(t.SchemaOnly, ", "))
		}
	}
	if sc.Base != "" || sc.ChainDepth > 0 {
		base := sc.Base
		if base == "" {
			base = "(root full)"
		}
		fmt.Fprintf(os.Stderr, "  chain    : depth %d, base %s\n", sc.ChainDepth, base)
	}
	if e := sc.Encryption; e != nil && e.Type != "" {
		line := e.Type
		if len(e.Recipients) > 0 {
			line += fmt.Sprintf(", %d recipient(s)", len(e.Recipients))
		}
		if e.Passphrase {
			line += ", passphrase-protected"
		}
		fmt.Fprintf(os.Stderr, "  encrypt  : %s\n", line)
	}
	if sc.Compress != "" {
		lvl := ""
		if sc.CompressLevel > 0 {
			lvl = fmt.Sprintf(" level %d", sc.CompressLevel)
		}
		fmt.Fprintf(os.Stderr, "  compress : %s%s\n", sc.Compress, lvl)
	}
	if sc.DurationMs > 0 {
		fmt.Fprintf(os.Stderr, "  duration : %dms (backup pipeline)\n", sc.DurationMs)
	}
	if sc.RawSize > 0 {
		fmt.Fprintf(os.Stderr, "  raw size : %s\n", formatBytes(sc.RawSize))
	}
	if len(sc.Checksums) > 0 {
		keys := make([]string, 0, len(sc.Checksums))
		for k := range sc.Checksums {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(os.Stderr, "  checksums: %s\n", strings.Join(keys, ", "))
	}
	for _, n := range sc.Notes {
		fmt.Fprintf(os.Stderr, "  note     : %s\n", n)
	}
}
func progressMetersEnabled() bool {
	if globalProgress != nil {
		return *globalProgress
	}
	return isTerminal(os.Stderr)
}

func promptSecret(rd *bufio.Reader, label string) string {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	if isTerminal(os.Stdin) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err == nil {
			return string(b)
		}
		return ""
	}
	line, _ := rd.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptSelect(rd *bufio.Reader, name string, options []option, def string) string {
	fmt.Fprintf(os.Stderr, "\n%s:\n", name)
	var defaultKey string
	for _, o := range options {
		if o.value == "" {
			continue
		}
		mark := ""
		if o.isDefault {
			mark = " *"
			defaultKey = o.key
		}
		fmt.Fprintf(os.Stderr, "  %s) %-30s %s\n", o.key, o.label+mark, o.value)
	}
	fmt.Fprintf(os.Stderr, "  q) Quit\n")

	for {
		fmt.Fprintf(os.Stderr, "Select [%s, q]: ", defaultKey)
		line := readLine(rd)
		line = strings.ToLower(line)
		if line == "" {
			line = defaultKey
		}
		if line == "q" || line == "quit" {
			fmt.Fprintln(os.Stderr, "Bye!")
			os.Exit(0)
		}
		for _, o := range options {
			if o.key == line && o.value != "" {
				return o.value
			}
		}
		fmt.Fprintf(os.Stderr, "Invalid option.\n")
	}
}

func promptValue(rd *bufio.Reader, name, parsedVal, parsedLabel string, mask bool) string {
	fmt.Fprintf(os.Stderr, "\n%s:\n", name)
	if parsedVal != "" {
		fmt.Fprintf(os.Stderr, "  P) %s: '%s'\n", parsedLabel, parsedVal)
	}
	fmt.Fprintf(os.Stderr, "  E) Environment variable\n")
	fmt.Fprintf(os.Stderr, "  F) Read from file\n")
	fmt.Fprintf(os.Stderr, "  C) Custom value\n")
	fmt.Fprintf(os.Stderr, "  Q) Quit\n")

	defaultKey := "c"
	if parsedVal != "" {
		defaultKey = "p"
	}
	readEntry := func(prompt string) string {
		fmt.Fprintf(os.Stderr, "%s", prompt)
		if mask && isTerminal(os.Stdin) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err == nil {
				return string(b)
			}
			return ""
		}
		return readLine(rd)
	}

	for {
		fmt.Fprintf(os.Stderr, "Select [%s]: ", defaultKey)
		line := strings.ToLower(readLine(rd))
		if line == "" {
			line = defaultKey
		}
		switch line {
		case "q", "quit":
			fmt.Fprintln(os.Stderr, "Bye!")
			os.Exit(0)
		case "p", "parsed":
			if parsedVal != "" {
				return parsedVal
			}
		case "e", "env":
			fmt.Fprintf(os.Stderr, "Environment variable name: ")
			varName := strings.TrimSpace(readLine(rd))
			if varName == "" {
				continue
			}
			val := os.Getenv(varName)
			if val == "" {
				fmt.Fprintf(os.Stderr, "WARNING: %s is empty or unset.\n", varName)
				continue
			}
			if !mask {
				fmt.Fprintf(os.Stderr, "  resolved: %s=%s\n", varName, val)
			}
			return val
		case "f", "file":
			fmt.Fprintf(os.Stderr, "Enter file path: ")
			v := readLine(rd)
			if v != "" {
				b, err := os.ReadFile(v)
				if err != nil {
					fmt.Fprintf(os.Stderr, "ERROR reading file: %v\n", err)
					continue
				}
				return strings.TrimSpace(string(b))
			}
		case "c", "custom":
			v := readEntry("Enter " + name + ": ")
			if resolved, isVar := expandEnvValue(v); isVar {
				if resolved == "" {
					fmt.Fprintf(os.Stderr, "WARNING: %s is empty or unset.\n", v)
					continue
				}
				if !mask {
					fmt.Fprintf(os.Stderr, "  resolved %s -> %s\n", v, resolved)
				} else {
					fmt.Fprintf(os.Stderr, "  resolved %s -> ********\n", v)
				}
				v = resolved
			}
			return v
		default:
			fmt.Fprintf(os.Stderr, "Invalid option.\n")
		}
	}
}

func readLine(rd *bufio.Reader) string {
	s, err := rd.ReadString('\n')
	if err != nil {
		os.Exit(0)
	}
	return strings.TrimSpace(s)
}
func restoreTypeOptions(defaultType string) []option {
	opts := []option{{key: "p", label: "Parsed from filename", value: defaultType, isDefault: defaultType != ""}}
	keys := "abcdefghijklmnopqrstuvwxyz"
	idx := 0
	for _, spec := range database.EngineSpecs() {
		label := spec.Label
		if label == "" {
			label = spec.Name
		}
		opts = append(opts, option{key: string(keys[idx%len(keys)]), label: label, value: spec.Name})
		idx++
	}
	return opts
}
func startMeter(prefix string, n *atomic.Int64) chan struct{} {
	stop := make(chan struct{})
	if !progressMetersEnabled() {
		return stop
	}
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "\r%s%s", prefix, formatBytes(n.Load()))
			}
		}
	}()
	return stop
}

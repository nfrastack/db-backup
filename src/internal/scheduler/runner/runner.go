// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nfrastack/db-backup/internal/checksum"
	"github.com/nfrastack/db-backup/internal/compress"
	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/database/common"
	"github.com/nfrastack/db-backup/internal/encrypt"
	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/retention"
	"github.com/nfrastack/db-backup/internal/storage"
)

type connectivitySetter interface {
	SetConnectivity(*config.ConnectivityConfig)
}

type countingReader struct {
	r io.Reader
	n int64
}

type countingWriter struct{ n int64 }

type OutcomeSink func(dbType, trigger string, maintenance bool, ok bool, duration time.Duration)

var (
	recordOutcome OutcomeSink
	dryRun        atomic.Bool
)

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func DryRun() bool { return dryRun.Load() }

func encryptionAllowed(encType encrypt.Type) error {
	if encType == encrypt.None || encType == encrypt.Age {
		return nil
	}
	return license.AllowEncryption()
}

func incrementalAllowed() error {
	return license.AllowIncremental()
}

func resolveCompressionThreads(requested int, supported bool) int {
	if requested < 0 {
		return 1
	}
	return requested
}

func Run(ctx context.Context, job config.JobConfig, trigger string) (err error) {
	if log.Session() == "" {
		log.SetSession(RandomID(3))
	}
	if job.RunID == "" {
		job.RunID = RandomID(4)
	}
	start := time.Now()
	outcome := recordOutcome

	defer func() {
		if outcome != nil {
			outcome(job.Type, trigger, job.Maintenance != "", err == nil, time.Since(start))
		}
	}()

	pass := ResolveSecret(job.Pass)
	port := job.Port
	if port == 0 {
		port = DefaultPort(job.Type)
	}

	dbName := ""
	if job.Databases != nil && len(job.Databases.Include) > 0 {
		dbName = strings.Join(job.Databases.Include, ",")
	}

	if job.Databases != nil && len(job.Databases.Include) > 1 &&
		containsGlobals(job.Databases.Include) {
		var rest []string
		for _, d := range job.Databases.Include {
			if !strings.Contains(d, "__globals__") {
				rest = append(rest, d)
			}
		}
		if len(rest) > 0 {
			sub := job
			sub.Databases = &config.DatabaseList{Include: rest}
			if err := Run(ctx, sub, trigger); err != nil {
				return err
			}
		}
		globJob := job
		globJob.Databases = &config.DatabaseList{Include: []string{"__globals__"}}
		if err := Run(ctx, globJob, trigger); err != nil {
			return err
		}
		return nil
	}

	if _, err := database.BuildTLSConfig(job.TLS); err != nil {
		return LogFail(job, "backup failed", "tls", err)
	}

	if !job.SplitDB {
		RunHooks(job, "pre", "backup", dbName, nil)
	}

	if job.SplitDB && job.Databases != nil && len(job.Databases.Include) == 1 && strings.EqualFold(job.Databases.Include[0], "ALL") {
		JLog(log.LevelDebug, job, "listing databases",
			"status", "debug", "step", "list", "target", fmt.Sprintf("%s://%s:%d", job.Type, job.Host, port))
		dbs, err := database.ListDatabases(job.Type, job.Host, port, job.User, pass, job.AuthSource, job.TLS)
		if err != nil {
			return LogFail(job, "backup failed", "list", err)
		}
		JLog(log.LevelDebug, job, "listed databases",
			"status", "debug", "step", "list", "count", len(dbs))
		JLog(log.LevelInfo, job, "splitting into per-database backups",
			"status", "starting", "split", "true", "dbs", len(dbs))
		for _, name := range dbs {
			sub := job
			sub.Databases = &config.DatabaseList{Include: []string{name}}
			sub.SplitDB = false
			if err := Run(ctx, sub, trigger); err != nil {
				return err
			}
		}

		if strings.HasPrefix(strings.ToLower(job.Type), "postgres") {
			globJob := job
			globJob.Databases = &config.DatabaseList{Include: []string{"__globals__"}}
			globJob.SplitDB = false
			if err := Run(ctx, globJob, trigger); err != nil {
				return err
			}
		}
		return nil
	}

	ct := compress.Parse(job.Compression.Type)
	csType := checksum.Parse(job.Checksum)
	storageBackend := job.Storage.Backend
	storagePath := job.Storage.Path
	if storagePath == "" && strings.EqualFold(storageBackend, "filesystem") {
		return LogFail(job, "backup failed", "storage", errors.New("no storage path configured (set --storage-path or storage.path)"))
	}

	now := time.Now()
	if FilenameLoc != nil {
		now = now.In(FilenameLoc)
	}

	if job.Backup == nil {
		job.Backup = &config.BackupConfig{}
	}
	strat := job.Backup.EffectiveStrategy()

	if dbName == "__globals__" && strat != "full" {
		JLog(log.LevelWarn, job, "globals always use full strategy - ignoring incremental/differential",
			"status", "warn", "strategy", strat, "db", dbName)
		strat = "full"
	}

	if strat != "full" && job.Databases != nil && len(job.Databases.Include) == 1 && strings.EqualFold(job.Databases.Include[0], "ALL") && !job.SplitDB {
		return LogFail(job, "backup failed", "strategy",
			fmt.Errorf("strategy %q is not supported with databases.include: [ALL] without split_db: true - set split_db: true to run per-database %s backups", strat, strat))
	}

	if strat != "full" && job.Databases != nil && len(job.Databases.Include) > 1 && containsGlobals(job.Databases.Include) && !job.SplitDB {
		for _, inc := range job.Databases.Include {
			if strings.EqualFold(inc, "ALL") {
				return LogFail(job, "backup failed", "strategy",
					fmt.Errorf("strategy %q is not supported with databases.include: [ALL] without split_db: true - set split_db: true to run per-database %s backups", strat, strat))
			}
		}
	}

	configuredStrat := strat

	encType := encrypt.Parse(job.EncryptionType)
	if err := encryptionAllowed(encType); err != nil {
		return LogFail(job, "backup failed", "encryption",
			fmt.Errorf("%s encryption is not available in the Community edition (%s)", encType, err))
	}

	switch strings.ToLower(storageBackend) {
	case "azure", "gcs":
		if err := license.AllowStorage(); err != nil {
			g := license.Enabled(license.FeatureStorage)
			license.Notice(license.FeatureStorage, g)
			return LogFail(job, "backup failed", "storage",
				fmt.Errorf("%s storage is not available in the Community edition (%s)", storageBackend, err))
		}
	}

	if job.Storage.User != "" {
		currentUser := os.Getenv("USER")
		if currentUser == "" {
			currentUser = "root"
		}
		if currentUser != "root" && currentUser != job.Storage.User {
			JLog(log.LevelWarn, job, "running user may not own written files",
				"status", "warn", "user", currentUser, "storage_user", job.Storage.User)
		}
	}

	var st storage.Storage
	if dryRun.Load() {
		JLog(log.LevelInfo, job, "dry-run: skipping storage backend initialisation",
			"status", "starting", "step", "storage", "backend", storageBackend)
	} else {
		var err error
		st, err = storage.New(storage.Backend(storageBackend), StorageOpts(job.Storage))
		if err != nil {
			return LogFail(job, "backup failed", "storage", err)
		}
	}

	if strat != "full" && dbName != "" {
		if err := license.AllowIncremental(); err != nil {
			g := license.Enabled(license.FeatureIncremental)
			license.Notice(license.FeatureIncremental, g)
			JLog(log.LevelWarn, job, "incremental/differential is not available in the Community edition - falling back to a full backup",
				"status", "warn", "strategy", strat, "reason", err.Error())
			strat = "full"
		} else if !incrementalEngineRegistered(job.Type) {
			return LogFail(job, "backup failed", "strategy",
				fmt.Errorf("strategy %q is not supported for %s (incremental/differential not implemented - use strategy: full)", strat, job.Type))
		} else if err := database.CheckIncrementalSupport(ctx, job.Type, job.Host, port, job.User, pass, dbName, job.AuthSource, job.TLS); err != nil {
			JLog(log.LevelWarn, job, "incremental support unavailable, falling back to full backup",
				"status", "warn", "strategy", strat, "error", err.Error())
			strat = "full"
		} else {
			JLog(log.LevelDebug, job, "incremental support probe passed",
				"status", "debug", "step", "position", "strategy", strat, "engine", job.Type)
		}
	}

	chainDepth := 0
	if strat != "full" {
		chain := chainInfo(st, job.Type, dbName, job.Host)
		chainDepth = chain.Depth
		if reason := shouldResetChain(job.Backup, chain); reason != "" {
			JLog(log.LevelInfo, job, "full backup (chain reset)",
				"status", "starting", "strategy", strat, "full_reset", true, "reason", reason, "chain_depth", chain.Depth)
			strat = "full"
		}
	}

	since := ""
	if strat != "full" && positionAnchored(job.Type) {
		since = findLastPosition(st, job.Type, dbName, job.Host, strat)
		if since == "" {
			JLog(log.LevelWarn, job, "no incremental anchor found, falling back to full backup",
				"status", "warn", "configured_strategy", configuredStrat,
				"reason", "no previous backup with a recorded position (binary logging disabled or chain purged?)")
			strat = "full"
		}
	}

	filename := expandFilename(job.Backup.Filename, job, dbName, strat, now)
	filename += database.FormatExtension(job.Type)
	filename += compress.Extension(ct)
	if encType != encrypt.None {
		filename += encrypt.Extension(encType)
	}
	if !dryRun.Load() {
		if entries, err := st.List(ctx, ""); err == nil {
			taken := make(map[string]bool, len(entries))
			for _, e := range entries {
				taken[e.Path] = true
				taken[strings.TrimSuffix(e.Path, ".json")] = true
			}
			if final := database.UniqueBackupName(filename, func(c string) bool { return taken[c] }); final != filename {
				JLog(log.LevelWarn, job, "backup filename collision - renaming",
					"status", "warn", "step", "upload", "original", filename, "renamed", final)
				filename = final
			}
		}
	}

	baseFile := ""
	if !dryRun.Load() && strat != "full" {
		if strat == "differential" {
			baseFile = findLastFullBackup(st, job.Type, dbName, job.Host)
		} else {
			baseFile = findLastChainParent(st, job.Type, dbName, job.Host)
		}
		if baseFile != "" {
			JLog(log.LevelDebug, job, "chain parent resolved",
				"status", "debug", "step", "position", "base", baseFile)
		}
	}

	hookStatus := "failed"
	defer func() {
		RunHooks(job, "post", "backup", dbName, map[string]string{
			"DBB_STATUS":   hookStatus,
			"DBB_FILENAME": filename,
		})
	}()

	encHasher, err := checksum.New(csType, io.Discard)
	if err != nil {
		return LogFail(job, "backup failed", "checksum", err)
	}

	rawHasher, err := checksum.New(csType, io.Discard)
	if err != nil {
		return LogFail(job, "backup failed", "checksum", err)
	}

	comp, err := compress.New(ct)
	if err != nil {
		return LogFail(job, "backup failed", "compress", err)
	}

	compOpts := compress.Options{Threads: job.Compression.Threads, Rsyncable: job.Compression.Rsyncable}
	if ct == compress.XZip || ct == compress.Bzip2 {
		if compOpts.Threads > 1 || compOpts.Rsyncable {
			JLog(log.LevelWarn, job, "parallel compression / rsyncable not supported for "+string(ct)+" - ignoring",
				"status", "warn", "compression", string(ct))
			compOpts = compress.Options{}
		}
	} else {
		compOpts.Threads = resolveCompressionThreads(compOpts.Threads, true)
	}

	enc, err := encrypt.New(encType, job.EncryptionIdentity, job.EncryptionIdentities...)
	if err != nil {
		return LogFail(job, "backup failed", "encrypt", err)
	}

	if encType != encrypt.None {
		JLog(log.LevelInfo, job, "encrypting backup",
			"status", "starting", "encryption", string(encType))
	}

	if common.MethodOf(job.Connectivity) != config.MethodNone {
		if strings.EqualFold(job.Type, "sqlite") || strings.EqualFold(job.Type, "sqlite3") {
			path := job.Host
			if path == "localhost" || path == "" {
				path = dbName
			}
			if err := common.FilePerms(path); err != nil {
				return LogFail(job, "backup failed", "connect", err)
			}
		} else {
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := common.TCPDialContext(checkCtx, job.Host, port)
			cancel()
			if err != nil {
				return LogFail(job, "backup failed", "connect", err)
			}
		}
	}

	opStart := time.Now()

	checkSpoolSpace(job)

	prog := newProgress(job, storagePath+"/"+filename, job.Progress)
	prog.start()
	defer prog.finish()
	var onTable func(db, table string)
	if prog.on {
		onTable = prog.setTable
	}

	pr, pw := io.Pipe()
	type stageTimings struct {
		dump, comp, enc time.Duration
		rawSize         int64
	}
	timingCh := make(chan stageTimings, 1)
	JLog(log.LevelDebug, job, "connecting to database",
		"status", "debug", "step", "connect", "target", fmt.Sprintf("%s://%s:%d", job.Type, job.Host, port), "engine", job.Type)
	go func() {
		encWriter, err := enc.Encrypt(pw)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("encrypt: %w", err))
			return
		}
		encMW := io.MultiWriter(encWriter, encHasher)

		compWriter, err := comp.Compress(encMW, job.Compression.Level, compOpts)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("compress: %w", err))
			return
		}
		rawCounter := &countingWriter{}
		mw := io.MultiWriter(compWriter, rawHasher, rawCounter)

		tDumpStart := time.Now()
		var tableFilter *config.TableFilter
		if job.Databases != nil && job.Databases.Tables != nil {
			tableFilter = job.Databases.Tables
		}
		globalSchemaOnly := job.Backup != nil && job.Backup.SchemaOnly
		if strat == "full" {
			JLog(log.LevelDebug, job, "dumping database",
				"status", "debug", "step", "dump", "db", dbName, "target", fmt.Sprintf("%s://%s:%d", job.Type, job.Host, port))
			if err := dumpTo(ctx, mw, job, port, pass, dbName, tableFilter, globalSchemaOnly, job.TLS, onTable); err != nil {
				pw.CloseWithError(err)
				return
			}
		} else {
			JLog(log.LevelDebug, job, "running incremental/differential dump",
				"status", "debug", "step", "dump", "strategy", strat, "since", since, "chain_depth", chainDepth)
			if err := database.RunBackup(ctx, mw, database.BackupOptions{
				Type:       job.Type,
				Host:       job.Host,
				Port:       port,
				User:       job.User,
				Pass:       pass,
				DB:         dbName,
				AuthSource: job.AuthSource,
				TLS:        job.TLS,
				Strategy:   database.Strategy(strat),
				Since:      since,
			}); err != nil {
				pw.CloseWithError(fmt.Errorf("incremental %s: %w", strat, err))
				return
			}
		}
		tDumpEnd := time.Now()

		if err := compWriter.Close(); err != nil {
			pw.CloseWithError(fmt.Errorf("compress close: %w", err))
			return
		}
		tCompEnd := time.Now()
		if timingsEnabled(job) {
			JLog(log.LevelDebug, job, "compressed data",
				"status", "debug", "step", "compress", "bytes", rawCounter.n, "timing", timingField("duration", tCompEnd.Sub(tDumpEnd)))
		}

		if err := encWriter.Close(); err != nil {
			pw.CloseWithError(fmt.Errorf("encrypt close: %w", err))
			return
		}
		tEncEnd := time.Now()
		if encType != encrypt.None && timingsEnabled(job) {
			JLog(log.LevelDebug, job, "encrypted data",
				"status", "debug", "step", "encrypt", "type", string(encType), "timing", timingField("duration", tEncEnd.Sub(tCompEnd)))
		}

		pw.Close()

		timingCh <- stageTimings{
			dump:    tDumpEnd.Sub(tDumpStart),
			comp:    tCompEnd.Sub(tDumpEnd),
			enc:     tEncEnd.Sub(tCompEnd),
			rawSize: rawCounter.n,
		}
	}()

	uploadStart := time.Now()
	var n int64
	if dryRun.Load() {
		cr := &countingReader{r: prog.reader(pr)}
		_, _ = io.Copy(io.Discard, cr)
		n = cr.n
		JLog(log.LevelInfo, job, "dry-run: skipped upload",
			"status", "complete", "step", "upload", "target", storagePath+"/"+filename, "bytes", n)
	} else {
		JLog(log.LevelDebug, job, "writing backup to storage",
			"status", "debug", "step", "upload", "target", storagePath+"/"+filename)
		cr := &countingReader{r: prog.reader(pr)}
		var err error
		n, err = st.Upload(ctx, filename, cr)
		if n == 0 {
			n = cr.n
		}
		if err != nil {
			pr.Close()
			prog.finish()
			select {
			case <-timingCh:
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
			}
			return LogFail(job, "backup failed", "upload", err)
		}
	}
	uploadTime := time.Since(uploadStart)
	if timingsEnabled(job) {
		JLog(log.LevelDebug, job, "uploaded backup",
			"status", "debug", "step", "upload", "bytes", n, "timing", timingField("duration", uploadTime))
	}
	pr.Close()
	prog.finish()
	var timing stageTimings
	select {
	case timing = <-timingCh:
	case <-time.After(5 * time.Second):
		JLog(log.LevelWarn, job, "timing collection timed out",
			"status", "warn", "step", "timing")
	case <-ctx.Done():
		JLog(log.LevelWarn, job, "timing collection cancelled",
			"status", "warn", "step", "timing")
	}
	totalTime := time.Since(opStart)

	fields := []any{
		"status", "complete",
		"strategy", strat,
		"file", storagePath + "/" + filename,
		"size", formatSize(n, sizeUnit(job)),
	}
	if timingsEnabled(job) {
		var pairs []any
		pairs = append(pairs, "dump", timing.dump)
		if ct != compress.None {
			pairs = append(pairs, "comp", timing.comp)
		}
		if encType != encrypt.None {
			pairs = append(pairs, "encrypt", timing.enc)
		}
		pairs = append(pairs, "upload", uploadTime, "total", totalTime)
		fields = append(fields, "timing", timingField(pairs...))
	}
	JLog(log.LevelInfo, job, "backup complete", fields...)

	if !dryRun.Load() {
		createLatestSymlink(job, dbName, storagePath, filename)
	}

	if csType != checksum.None {
		sum := encHasher.Sum()
		rawSum := rawHasher.Sum()
		chks := map[string]string{
			"compressed_" + job.Checksum:   sum,
			"uncompressed_" + job.Checksum: rawSum,
		}

		if configuredStrat != "full" && strings.HasPrefix(strings.ToLower(job.Type), "postgres") &&
			dbName != "" && dbName != "__globals__" && dbName != "ALL" {
			if err := incrementalAllowed(); err != nil {
				JLog(log.LevelDebug, job, "postgres logical slots skipped - incremental is not available in the Community edition",
					"status", "debug", "reason", err.Error())
			} else if err := database.PgEnsureLogicalSlots(ctx, job.Host, port, job.User, pass, dbName); err != nil {
				JLog(log.LevelDebug, job, "postgres logical slots", "status", "debug", "error", err.Error())
			} else {
				JLog(log.LevelDebug, job, "postgres logical slots ready",
					"status", "debug", "slots", []string{"dbbackup_incr_" + dbName, "dbbackup_diff_" + dbName})
			}
		}

		sc := &retention.Sidecar{
			Base:          baseFile,
			Format:        retention.FormatName,
			SchemaVersion: retention.SchemaVersion,
			Tool: &retention.ToolMeta{
				Name:     "db-backup",
				Version:  version,
				Edition:  license.Edition(),
				Hostname: Hostname(),
			},
			Trigger:         trigger,
			SessionID:       log.Session(),
			RunID:           job.RunID,
			Status:          "ok",
			DurationMs:      totalTime.Milliseconds(),
			RawSize:         timing.rawSize,
			Strategy:        strat,
			Type:            job.Type,
			DB:              dbName,
			Host:            job.Host,
			Timestamp:       now.Format(time.RFC3339),
			Checksums:       chks,
			Size:            n,
			Compress:        string(ct),
			CompressLevel:   job.Compression.Level,
			CompressThreads: compOpts.Threads,
			Rsyncable:       job.Compression.Rsyncable,
			ChainDepth:      chainDepth,
			FileName:        filename,
		}

		if job.Name != "" {
			sc.Job = &retention.JobMeta{
				Name:     job.Name,
				Schedule: job.Schedule.Describe(),
			}
		}

		if job.Backup != nil && job.Backup.SchemaOnly {
			sc.SchemaOnly = true
		}
		if job.Databases != nil && job.Databases.Tables != nil {
			tf := job.Databases.Tables
			tm := &retention.TableMeta{}
			if len(tf.Include) > 0 {
				tm.Include = tf.Include
			}
			if len(tf.Exclude) > 0 {
				tm.Exclude = tf.Exclude
			}
			if tf.SchemaOnly.All {
				tm.SchemaOnly = []string{"*"}
			} else if len(tf.SchemaOnly.Tables) > 0 {
				tm.SchemaOnly = tf.SchemaOnly.Tables
			}
			if tm.Include != nil || tm.Exclude != nil || tm.SchemaOnly != nil {
				sc.Tables = tm
			}
		}

		if sc.SchemaOnly && (strings.HasPrefix(strings.ToLower(job.Type), "mongo") || strings.HasPrefix(strings.ToLower(job.Type), "redis")) {
			msg := "schema_only ignored for " + job.Type + " (no schema)"
			JLog(log.LevelWarn, job, "schema_only ignored", "status", "warn", "detail", msg)
			sc.Notes = append(sc.Notes, msg)
		}

		if encType != encrypt.None {
			em := &retention.EncryptionMeta{
				Type: string(encType),
			}
			if job.EncryptionIdentity != "" {
				em.Passphrase = true
			} else {
				em.Recipients = job.EncryptionIdentities
			}
			sc.Encryption = em
		}

		if !dryRun.Load() && configuredStrat != "full" {
			if err := incrementalAllowed(); err != nil {
				JLog(log.LevelDebug, job, "position capture skipped - incremental is not available in the Community edition",
					"status", "debug", "step", "position", "reason", err.Error())
			} else {
				tPos := time.Now()
				if pos, err := database.GetPosition(ctx, job.Type, job.Host, port, job.User, pass, dbName, job.AuthSource, job.TLS); err == nil && pos != "" {
					sc.Position = pos
					if timingsEnabled(job) {
						JLog(log.LevelDebug, job, "captured position",
							"status", "debug", "step", "position", "position", pos, "timing", timingField("duration", time.Since(tPos)))
					}
				} else if err != nil {
					JLog(log.LevelDebug, job, "backup position not recorded",
						"status", "debug", "step", "position", "error", err.Error())
				}
			}
		}
		if !dryRun.Load() {
			tSidecar := time.Now()
			if err := retention.WriteSidecar(st, filename, sc); err != nil {
				JLog(log.LevelWarn, job, "sidecar write failed", "status", "warn", "error", err.Error())
			}
			if timingsEnabled(job) {
				JLog(log.LevelDebug, job, "sidecar written",
					"status", "debug", "step", "sidecar",
					"file", filename+".json", "timing", timingField("duration", time.Since(tSidecar)))
			}
		}
	}
	hookStatus = "ok"
	return nil
}

func SetDryRun(v bool) { dryRun.Store(v) }

func SetOutcomeSink(f OutcomeSink) { recordOutcome = f }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

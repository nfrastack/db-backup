// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
	sched "github.com/nfrastack/db-backup/internal/scheduler"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
)

func cmdScheduler(args []string) int {
	fs := flag.NewFlagSet("scheduler", flag.ExitOnError)
	configFile := fs.String("config", "", "Config file")
	once := fs.Bool("once", false, "Run each job once and exit")
	concurrency := fs.Int("concurrency", 0, "Max simultaneous backup runs (0 = unlimited)")
	fs.Parse(args)

	cfgPaths := globalConfigPaths
	if *configFile != "" {
		cfgPaths = []string{*configFile}
	}

	if len(cfgPaths) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: no config file found - pass -c /path/to/db-backup.yaml (looked in the working directory and /etc)\n")
		return 1
	}

	cfg, err := config.LoadConfig(cfgPaths...)
	if err != nil {
		log.Error("scheduler", fmt.Sprintf("config: %v", err))
		return 1
	}

	if *concurrency > 0 {
		cfg.Concurrency = *concurrency
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reportDone := startReportLoop(ctx, cfg)

	log.SetSession(runner.RandomID(3))
	if globalContainer || globalSystemd || !isTerminal(os.Stdout) {
		log.Info("startup", fmt.Sprintf("db-backup %s | build=%s mode=%s | © 2026 Nfrastack https://nfrastack.com", Version, buildEdition, runtimeMode()),
			"host", runner.Hostname())
		if note := runtimeNote(); note != "" {
			log.Info("startup", note)
		}
		if buildEdition == "supporter" && runtimeMode() == "supporter" {
			log.Info("startup", "for implementation support and consulting visit: https://nfrastack.com/db-backup")
		}
		log.Info("startup", startupLog())
		if statsMgr != nil {
			statsMgr.LogStartup()
		}
	} else {
		printBanner()
		log.Info("scheduler", startupLog())
		if statsMgr != nil {
			statsMgr.LogStartup()
		}
	}

	logSupporterNudge(len(cfg.Jobs))

	run := func(job config.JobConfig) error {
		return runner.Run(ctx, job, "scheduled")
	}
	sch := sched.NewWithRunner(cfg, run)
	if statsTracker != nil {
		sch.SetActivitySink(statsTracker.RecordActivity)
	}
	code := 0
	if *once {
		code = sch.RunOnce()
	} else {
		code = sch.Run()
	}
	if reportDone != nil {
		select {
		case <-reportDone:
		case <-time.After(3 * time.Second):
			log.Warn("scheduler", "usage stats report still submitting - exiting without waiting")
		}
	}
	cancel()
	if code != 0 {
		return code
	}
	return 0
}

const reportLoopInterval = time.Hour

func startReportLoop(ctx context.Context, cfg *config.Config) <-chan struct{} {
	if statsMgr == nil || !(statsMgr.Enabled() || statsMgr.VersionCheckEnabled()) {
		return nil
	}
	done := make(chan struct{})
	go func() {
		statsMgr.TryReport(ctx, statsOpts(), cfg, statsTracker)
		close(done)
		ticker := time.NewTicker(reportLoopInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				statsMgr.TryReport(ctx, statsOpts(), cfg, statsTracker)
			}
		}
	}()
	return done
}

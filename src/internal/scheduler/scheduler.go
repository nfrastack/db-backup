// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
)

type Runner func(job config.JobConfig) error

type ActivitySink func(dbType, op string, n int)

type Scheduler struct {
	cfg      *config.Config
	wg       sync.WaitGroup
	run      Runner
	failed   atomic.Int32
	sess     string
	slots    chan struct{}
	activity ActivitySink
}

const (
	reminderInterval = 24 * time.Hour
	startupStagger   = 100 * time.Millisecond
)

func NewWithRunner(cfg *config.Config, run Runner) *Scheduler {
	return &Scheduler{
		cfg: cfg,
		run: run,
	}
}

// wire retention activity reporting for usage stats

func (s *Scheduler) Run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.SetSession(s.session())

	if s.cfg != nil && s.cfg.Concurrency > 0 {
		s.slots = make(chan struct{}, s.cfg.Concurrency)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Info("scheduler", fmt.Sprintf("starting %d job%s (concurrency=%d)", len(s.cfg.Jobs), plural(len(s.cfg.Jobs)), s.concurrencyValue()))

	if msg := s.editionNotice(); msg != "" {
		log.Warn("scheduler", msg)
	}

	recurring := 0
	for _, job := range s.cfg.Jobs {
		if job.Schedule != nil && job.Schedule.IsRecurring() {
			recurring++
		}
	}

	for i, job := range s.cfg.Jobs {
		s.wg.Add(1)
		go s.runJob(ctx, job, i)
	}

	if communityBuild {
		go func() {
			ticker := time.NewTicker(reminderInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					log.Info("scheduler", "for Implementation support and consulting visit: https://nfrastack.com/db-backup")
				}
			}
		}()
	}

	signalStopped := false
	if recurring == 0 {
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case sig := <-sigCh:
			log.Info("scheduler", fmt.Sprintf("received %s - shutting down", sig))
			signalStopped = true
			cancel()
			s.wg.Wait()
		}
	} else {
		sig := <-sigCh
		log.Info("scheduler", fmt.Sprintf("received %s - shutting down", sig))
		signalStopped = true
		cancel()
		s.wg.Wait()
	}

	failed := int(s.failed.Load())
	if failed > 0 {
		log.Error("scheduler", fmt.Sprintf("%d job%s failed", failed, plural(failed)))
		return 1
	}
	if signalStopped {
		log.Info("scheduler", "shutdown complete")
		return 0
	}
	log.Info("scheduler", "all jobs complete - exiting")
	return 0
}

func (s *Scheduler) RunOnce() int {
	log.SetSession(s.session())
	if s.cfg != nil && s.cfg.Concurrency > 0 && s.slots == nil {
		s.slots = make(chan struct{}, s.cfg.Concurrency)
	}
	for _, job := range s.cfg.Jobs {
		job.RunID = newRunID()
		if skip, reason := s.slotGate(job, time.Now()); skip {
			jlog(log.LevelInfo, "scheduler", job, "job skipped", "status", "skipped", "reason", reason)
			continue
		}
		if s.runOnceJob(context.Background(), job, "") {
			s.failed.Add(1)
		}
	}

	failed := int(s.failed.Load())
	if failed > 0 {
		log.Error("scheduler", fmt.Sprintf("%d job%s failed", failed, plural(failed)))
		return 1
	}
	log.Info("scheduler", "all jobs complete")
	return 0
}

func (s *Scheduler) SetActivitySink(f ActivitySink) { s.activity = f }

func (s *Scheduler) concurrencyValue() int {
	if s.cfg == nil {
		return 0
	}
	return s.cfg.Concurrency
}

func (s *Scheduler) editionNotice() string {
	if communityBuild {
		return "db-backup Community edition - the Supported edition adds incremental backups, cloud storage, GPG/OpenSSL encryption, GFS retention, archive, and advanced maintenance (https://nfrastack.com/db-backup)"
	}
	return ""
}

func newRunID() string {
	return randomID(4)
}

func newSessionID() string {
	return randomID(3)
}

func randomID(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func (s *Scheduler) session() string {
	if s.sess == "" {
		if existing := log.Session(); existing != "" {
			s.sess = existing
		} else {
			s.sess = newSessionID()
		}
	}
	return s.sess
}

func (s *Scheduler) slotGate(job config.JobConfig, now time.Time) (skip bool, reason string) {
	if job.Schedule == nil {
		return false, ""
	}
	if !job.Schedule.DayAllowed(now) {
		return true, "not scheduled for today"
	}
	if job.Schedule.Blocked(now) {
		return true, "blackout window"
	}
	return false, ""
}

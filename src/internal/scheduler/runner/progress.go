// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/log"
	"golang.org/x/term"
)

type progress struct {
	job    config.JobConfig
	target string

	on      bool
	term    bool
	startAt time.Time
	lastN   int64
	lastAt  time.Time

	curTable string

	mu       sync.Mutex
	done     chan struct{}
	tick     *time.Ticker
	stop     chan struct{}
	every    time.Duration
	finished bool

	meterEvery time.Duration
}

type progressReader struct {
	p *progress
	r io.Reader
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	pr.p.add(int64(n))
	return n, err
}
func (p *progress) add(n int64) {
	if n <= 0 {
		return
	}
	p.mu.Lock()
	p.lastN += n
	p.mu.Unlock()
}

func (p *progress) finish() {
	p.mu.Lock()
	if !p.on || p.finished {
		p.mu.Unlock()
		return
	}
	p.finished = true
	stop := p.stop
	tick := p.tick
	done := p.done
	isTerm := p.term
	p.mu.Unlock()

	close(stop)
	if tick != nil {
		tick.Stop()
	}
	if isTerm {
		fmt.Fprint(os.Stderr, "\r\x1b[K\n")
	}
	close(done)
}
func newProgress(job config.JobConfig, target string, enabled *bool) *progress {
	p := &progress{job: job, target: target, every: 10 * time.Second, meterEvery: time.Second}
	p.term = term.IsTerminal(int(os.Stderr.Fd()))
	switch {
	case enabled != nil:
		p.on = *enabled
	default:
		p.on = p.term
	}
	return p
}

func (p *progress) reader(r io.Reader) io.Reader {
	if !p.on {
		return r
	}
	return &progressReader{p: p, r: r}
}

func (p *progress) render() {
	p.mu.Lock()
	n := p.lastN
	table := p.curTable
	now := time.Now()
	elapsed := now.Sub(p.startAt)
	rate := float64(n) / elapsed.Seconds()
	p.mu.Unlock()

	sz := humanSize(n)
	rateStr := fmt.Sprintf("%.1f MiB/s", rate/(1024*1024))
	elapsedStr := roundDur(elapsed)

	if p.term {
		line := fmt.Sprintf("[%s] %s @ %s", elapsedStr, sz, rateStr)
		if table != "" {
			line += "  (" + table + ")"
		}
		fmt.Fprintf(os.Stderr, "\r\x1b[K%s", line)
		return
	}

	fields := []any{
		"status", "progress",
		"step", "upload",
		"bytes", n,
		"rate", rateStr,
		"elapsed", elapsedStr,
	}
	if table != "" {
		fields = append(fields, "table", table)
	}
	JLog(log.LevelInfo, p.job, "backup in progress", fields...)
}

func (p *progress) renderLoop() {
	defer func() { recover() }()
	for {
		select {
		case <-p.stop:
			return
		case <-p.tick.C:
			p.render()
		}
	}
}
func (p *progress) setTable(db, table string) {
	if !p.on {
		return
	}
	p.mu.Lock()
	p.curTable = db + "." + table
	p.mu.Unlock()
}

func (p *progress) start() {
	if !p.on {
		return
	}
	p.mu.Lock()
	p.startAt = time.Now()
	p.lastAt = p.startAt
	p.done = make(chan struct{})
	p.stop = make(chan struct{})
	p.finished = false
	interval := p.every
	if p.term {
		interval = p.meterEvery
	}
	p.tick = time.NewTicker(interval)
	p.mu.Unlock()
	go p.renderLoop()
}

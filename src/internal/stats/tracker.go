// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"sync"
	"time"
)

// records job success/failure counts, run duration and retention activity per database type over a rolling 24h window
type Tracker struct {
	mu       sync.Mutex
	now      func() time.Time
	byTs     map[string]map[int64]*counts
	activity map[string]map[string]int64
}
type counts struct {
	success  int
	failed   int
	duration time.Duration
}

// mark records one run outcome for a database type. duration is the total time the run took (success or failure)
func (t *Tracker) Mark(dbType string, ok bool, duration time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	hour := t.now().Unix() / 3600
	if t.byTs == nil {
		t.byTs = make(map[string]map[int64]*counts)
	}
	bucket, ok2 := t.byTs[dbType]
	if !ok2 {
		bucket = make(map[int64]*counts)
		t.byTs[dbType] = bucket
	}
	c := bucket[hour]
	if c == nil {
		c = &counts{}
		bucket[hour] = c
	}
	if ok {
		c.success++
	} else {
		c.failed++
	}
	c.duration += duration
}

func NewTracker() *Tracker {
	return &Tracker{now: time.Now}
}

// increment prune or archive counter
func (t *Tracker) RecordActivity(dbType, op string, n int) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.activity == nil {
		t.activity = make(map[string]map[string]int64)
	}
	if t.activity[dbType] == nil {
		t.activity[dbType] = make(map[string]int64)
	}
	t.activity[dbType][op] += int64(n)
}

// returns per database type successes, failures, run duration and retention activity over rolling 24h window
func (t *Tracker) Snapshot() []JobOutcome {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	minHour := now.Unix()/3600 - 24
	var out []JobOutcome
	for dbType, buckets := range t.byTs {
		o := JobOutcome{Type: dbType}
		for hour, c := range buckets {
			if hour < minHour {
				delete(buckets, hour)
				continue
			}
			o.Success += c.success
			o.Failed += c.failed
			o.Duration += c.duration
		}
		if o.Success > 0 || o.Failed > 0 {
			out = append(out, o)
		}
	}
	for dbType, ops := range t.activity {
		o := JobOutcome{Type: dbType}
		o.Pruned = int(ops["prune"])
		o.Archived = int(ops["archive"])
		if o.Pruned > 0 || o.Archived > 0 {
			if i := outcomeIndex(out, dbType); i >= 0 {
				out[i].Pruned = o.Pruned
				out[i].Archived = o.Archived
			} else {
				out = append(out, o)
			}
		}
	}
	return out
}

func outcomeIndex(out []JobOutcome, dbType string) int {
	for i := range out {
		if out[i].Type == dbType {
			return i
		}
	}
	return -1
}

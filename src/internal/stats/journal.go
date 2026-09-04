// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// operation types
const (
	OpBackup      = "backup"
	OpRestore     = "restore"
	OpMaintenance = "maintenance"
	OpPrune       = "prune"
	OpArchive     = "archive"
	OpVerify      = "verify"

	TriggerScheduled = "scheduled"
	TriggerManual    = "manual"
)

type Event struct {
	Ts      string `json:"ts"`
	Op      string `json:"op"`
	Trigger string `json:"trig"`
	Engine  string `json:"eng,omitempty"`
	OK      bool   `json:"ok"`
	Ms      int64  `json:"ms,omitempty"`
	Jobs    int    `json:"n,omitempty"`
}

var (
	journalMu       sync.Mutex
	journalDir      string
	journalDisabled bool
)

// one operation/trigger bucket in a submit window
type Agg struct {
	Op      string `json:"op"`
	Trigger string `json:"trig,omitempty"`
	N       int    `json:"n"`
	OK      int    `json:"ok"`
	Fail    int    `json:"fail"`
}

// purge events after completion
func Ack(watermark time.Time) {
	journalMu.Lock()
	d := journalDir
	journalMu.Unlock()
	if d == "" {
		return
	}
	entries, _ := os.ReadDir(d)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		full := filepath.Join(d, name)
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		var keep []string
		for _, ln := range strings.Split(string(data), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			var ev Event
			if json.Unmarshal([]byte(ln), &ev) != nil || ev.Ts == "" {
				continue // unparsable junk rides along forever
			}
			ts, err := time.Parse(time.RFC3339Nano, ev.Ts)
			if err != nil || !ts.After(watermark) {
				continue
			}
			keep = append(keep, ln)
		}
		if len(keep) == 0 {
			_ = os.Remove(full)
			continue
		}
		tmp := full + ".tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")+"\n"), 0o600); err != nil {
			continue
		}
		_ = os.Rename(tmp, full)
	}
}

func Aggregate(events []Event) []Agg {
	type key struct{ op, trig string }
	order := make([]key, 0)
	idx := map[key]int{}
	var aggs []Agg
	for _, ev := range events {
		k := key{ev.Op, ev.Trigger}
		i, ok := idx[k]
		if !ok {
			i = len(aggs)
			idx[k] = i
			aggs = append(aggs, Agg{Op: ev.Op, Trigger: ev.Trigger})
			order = append(order, k)
		}
		aggs[i].N++
		if ev.OK {
			aggs[i].OK++
		} else {
			aggs[i].Fail++
		}
	}
	sort.SliceStable(aggs, func(i, j int) bool {
		if aggs[i].Op != aggs[j].Op {
			return aggs[i].Op < aggs[j].Op
		}
		return aggs[i].Trigger < aggs[j].Trigger
	})
	_ = order
	return aggs
}

// try to record event
func Record(op, trigger, engine string, ok bool, ms int64, jobs int) {
	journalMu.Lock()
	d, off := journalDir, journalDisabled
	journalMu.Unlock()
	if off || d == "" || op == "" {
		return
	}
	ev := Event{
		Ts:      time.Now().UTC().Format(time.RFC3339Nano),
		Op:      op,
		Trigger: trigger,
		Engine:  engine,
		OK:      ok,
		Ms:      ms,
		Jobs:    jobs,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	day := time.Now().UTC().Format("20060102")
	f := filepath.Join(d, "events-"+day+".ndjson")
	journalMu.Lock()
	defer journalMu.Unlock()
	_ = os.MkdirAll(d, 0o755)
	fp, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer fp.Close()
	_, _ = fp.Write(append(line, '\n'))
}

// fetch journaldir. empty diables
func SetJournalDir(path string) {
	journalMu.Lock()
	defer journalMu.Unlock()
	journalDir = path
}

// journal only can operate when stats.enabled=true
func SetJournalDisabled(d bool) {
	journalMu.Lock()
	defer journalMu.Unlock()
	journalDisabled = d
}

// reads all events with ts strictly after since.
func Window(since time.Time) []Event {
	journalMu.Lock()
	d := journalDir
	journalMu.Unlock()
	if d == "" {
		return nil
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "events-") && strings.HasSuffix(e.Name(), ".ndjson") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out []Event
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(d, name))
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(data), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			var ev Event
			if json.Unmarshal([]byte(ln), &ev) != nil || ev.Ts == "" {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, ev.Ts)
			if err != nil || !ts.After(since) {
				continue
			}
			out = append(out, ev)
		}
	}
	return out
}

// renders the compact o= token body: op[:trig]:ok:fail:n joined by |.
func (a Agg) Wire() string {
	s := a.Op
	if a.Trigger != "" {
		s += ":" + a.Trigger
	}
	return s
}

// renders the o= value for a window ("backup:scheduled:12:11|restore:manual:3:3")
func WireToken(events []Event) string {
	aggs := Aggregate(events)
	parts := make([]string, 0, len(aggs))
	for _, a := range aggs {
		s := a.Op
		if a.Trigger != "" {
			s += ":" + a.Trigger
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", s, a.OK, a.Fail))
	}
	return strings.Join(parts, "|")
}

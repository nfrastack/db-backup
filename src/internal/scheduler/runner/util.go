// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/log"
)

var FilenameLoc *time.Location
var version string

func DefaultPort(dbType string) int {
	return database.DefaultPort(dbType)
}

func Hostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return ""
}

func JLog(level log.Level, job config.JobConfig, msg string, fields ...any) {
	fields = append(jobRunFields(job), fields...)
	var colour *bool
	if job.Colour != nil {
		colour = job.Colour
	}
	log.WithOverridesTo(level, jobOverride(job), colour, job.LogPath, "backup", msg, fields...)
}

func LogFail(job config.JobConfig, msg, stage string, err error) error {
	if err == nil {
		err = fmt.Errorf("unknown error")
	}
	log.Error("backup", msg,
		append(jobRunFields(job), "status", "failed", "stage", stage, "error", err.Error())...)
	return err
}

func RandomID(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

func ResolveSecret(val string) string {
	return config.ResolveSecret(val)
}

func SetVersion(v string) { version = v }

func SplitCsv(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func StorageOpts(cfg *config.StorageConfig) map[string]string {
	return cfg.Options()
}
func formatSize(n int64, unit string) string {
	unit = strings.ToLower(strings.TrimSpace(unit))
	human := strings.HasSuffix(unit, "_human")
	base := strings.TrimSuffix(unit, "_human")

	if human {
		switch base {
		case "kb":
			return fmt.Sprintf("%.1f KiB", float64(n)/1024)
		case "mb":
			return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
		case "gb":
			return fmt.Sprintf("%.1f GiB", float64(n)/(1024*1024*1024))
		case "tb":
			return fmt.Sprintf("%.1f TiB", float64(n)/(1024*1024*1024*1024))
		default:
			return humanSize(n)
		}
	}

	switch base {
	case "kb":
		return fmt.Sprintf("%d", n/1024)
	case "mb":
		return fmt.Sprintf("%d", n/(1024*1024))
	case "gb":
		return fmt.Sprintf("%d", n/(1024*1024*1024))
	case "tb":
		return fmt.Sprintf("%d", n/(1024*1024*1024*1024))
	default:
		return fmt.Sprintf("%d", n)
	}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func jobField(name string) []any {
	if name != "" {
		return []any{"job", name}
	}
	return nil
}

func jobOverride(job config.JobConfig) log.Level {
	if job.LogLevel == "" {
		return 0
	}
	return log.ParseLevel(job.LogLevel)
}

func jobRunFields(job config.JobConfig) []any {
	fields := jobField(job.Name)
	if job.Name != "" {
		if job.MaintenanceCfg != nil {
			fields = append(fields, "type", "maintain")
		} else {
			fields = append(fields, "type", "backup")
		}
	}
	if job.RunID != "" {
		fields = append(fields, "run_id", job.RunID)
	}
	return fields
}

func roundDur(d time.Duration) string {
	d = d.Round(time.Millisecond)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	ms := d / time.Millisecond % 1000
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	if ms > 0 {
		return fmt.Sprintf("%d.%ds", s, ms/100)
	}
	return fmt.Sprintf("%ds", s)
}

func sizeUnit(job config.JobConfig) string {
	if job.SizeFormat != "" {
		return job.SizeFormat
	}
	if u := log.SizeFormat(); u != "" {
		return u
	}
	return "bytes"
}

func timingField(pairs ...any) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			continue
		}
		dur, ok := pairs[i+1].(time.Duration)
		if !ok {
			continue
		}
		parts = append(parts, key+"="+roundDur(dur))
	}
	return strings.Join(parts, " ")
}

func timingsEnabled(job config.JobConfig) bool {
	if job.Timings != nil {
		return *job.Timings
	}
	return log.TimingsEnabled()
}

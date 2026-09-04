// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// decode the payload for human review
type Decoded struct {
	SchemaVersion   int     `json:"schema_version"`
	InstanceID      string  `json:"instance_id"`
	Tool            string  `json:"tool"`
	Version         string  `json:"version"`
	ReportedAt      string  `json:"reported_at,omitempty"`
	PreviousVersion string  `json:"previous_version,omitempty"`
	Runtime         Runtime `json:"runtime"`
	UptimeHours     int     `json:"uptime_hours"`
	JobCount        int     `json:"job_count"`
	Log             Log     `json:"log"`
	ImageVersion    string  `json:"image_version,omitempty"`
	Jobs            []Job   `json:"jobs"`
}

// decoded 4 digit runtime code
type Runtime struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Container bool   `json:"container"`
	Systemd   bool   `json:"systemd"`
}

// decoded 3 digit log options code
type Log struct {
	Level       string `json:"level"`
	Format      string `json:"format"`
	Destination string `json:"destination"`
}

// decoded 15 field job entry
type Job struct {
	Database       string `json:"database"`
	Strategy       string `json:"strategy"`
	Compression    string `json:"compression"`
	Encryption     string `json:"encryption"`
	EncryptionMode string `json:"encryption_mode"`
	Storage        string `json:"storage"`
	Schedule       string `json:"schedule"`
	Maintenance    bool   `json:"maintenance"`
	Archive        bool   `json:"archive"`
	Successes      int    `json:"successes"`
	Failures       int    `json:"failures"`
	RetentionTiers string `json:"retention_tiers,omitempty"`
	DurationMs     int64  `json:"duration_ms"`
	Pruned         int    `json:"pruned"`
	Archived       int    `json:"archived"`
}

var (
	dbTypeNames = map[string]string{
		dbCouch:    "couchdb",
		dbInflux:   "influxdb",
		dbMSSQL:    "mssql",
		dbMariaDB:  "mariadb",
		dbMongo:    "mongodb",
		dbMySQL:    "mysql",
		dbPostgres: "postgresql",
		dbRedis:    "redis",
		dbSQLite:   "sqlite",
		dbOther:    "unknown engine",
	}
	strategyNames = map[string]string{
		stratFull:  "full",
		stratIncr:  "incremental",
		stratDiff:  "differential",
		stratOther: "unknown",
	}
	compressionNames = map[string]string{
		compNone:  "none",
		compGzip:  "gzip",
		compZstd:  "zstd",
		compXz:    "xz",
		compBz2:   "bzip2",
		compOther: "unknown",
	}
	encryptionNames = map[string]string{
		encNone:    "none",
		encAge:     "age",
		encOpenPGP: "openpgp",
		encOpenSSL: "openssl",
		encOther:   "unknown",
	}
	encryptionModeNames = map[string]string{
		encModeNone:       "none",
		encModeRecipients: "recipients",
		encModePassphrase: "passphrase",
		encModeIdentity:   "identity",
	}
	storageNames = map[string]string{
		storeFilesystem: "filesystem",
		storeS3:         "s3",
		storeAzure:      "azure",
		storeGCS:        "gcs",
		storeWebDAV:     "webdav",
		storeOther:      "unknown",
	}
	scheduleNames = map[string]string{
		schedManual:  "manual",
		schedHourly:  "hourly",
		schedDaily:   "daily",
		schedWeekly:  "weekly",
		schedMonthly: "monthly",
		schedCustom:  "custom",
	}
)

// expand every code to full name
func DecodePayload(payload string) (*Decoded, error) {
	d := &Decoded{}
	fields := strings.Fields(payload)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	seenSchema, seenTool := false, false
	for _, tok := range fields {
		eq := strings.IndexByte(tok, '=')
		if eq < 0 {
			continue
		}
		key, val := tok[:eq], tok[eq+1:]
		switch key {
		case "sv":
			d.SchemaVersion = atoiCode(val)
			seenSchema = true
		case "i":
			d.InstanceID = val
		case "t":
			d.Tool = toolName(val)
			seenTool = true
		case "v":
			d.Version = val
		case "d":
			d.ReportedAt = val
		case "p":
			d.PreviousVersion = val
		case "r":
			d.Runtime = decodeRuntime(val)
		case "u":
			d.UptimeHours = atoiCode(val)
		case "n":
			d.JobCount = atoiCode(val)
		case "l":
			d.Log = decodeLog(val)
		case "m":
			d.ImageVersion = val
		case "j":
			d.Jobs = decodeJobs(val)
		}
	}
	if !seenSchema || !seenTool {
		return nil, fmt.Errorf("not a usage-stats payload (missing schema version or tool id)")
	}
	return d, nil
}

// render it as json instead of text
func (d *Decoded) JSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

// looks for most recent stats dump file in the statsdump path, or error out if path or no files do not exist
func LatestDump() (string, error) {
	dir := DumpDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading dump directory %s: %w", dir, err)
	}
	var best string
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "stats-") || !strings.HasSuffix(name, ".nfrastat") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, name)
		if best == "" || info.ModTime().After(bestMod) {
			best, bestMod = path, info.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no stats dump files found in %s - run a job with usage stats enabled first", dir)
	}
	return best, nil
}

// render the payload
func (d *Decoded) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema_version    %d\n", d.SchemaVersion)
	fmt.Fprintf(&b, "instance_id       %s\n", d.InstanceID)
	fmt.Fprintf(&b, "tool              %s\n", d.Tool)
	fmt.Fprintf(&b, "version           %s\n", d.Version)
	if d.ReportedAt != "" {
		fmt.Fprintf(&b, "reported_at       %s\n", d.ReportedAt)
	}
	if d.PreviousVersion != "" {
		fmt.Fprintf(&b, "previous_version  %s\n", d.PreviousVersion)
	}
	fmt.Fprintf(&b, "os                %s_%s\n", d.Runtime.OS, d.Runtime.Arch)
	fmt.Fprintf(&b, "container         %t\n", d.Runtime.Container)
	fmt.Fprintf(&b, "systemd           %t\n", d.Runtime.Systemd)
	fmt.Fprintf(&b, "uptime_hours      %d\n", d.UptimeHours)
	fmt.Fprintf(&b, "log               level=%s format=%s destination=%s\n", d.Log.Level, d.Log.Format, d.Log.Destination)
	if d.ImageVersion != "" {
		fmt.Fprintf(&b, "image_version     %s\n", d.ImageVersion)
	}
	fmt.Fprintf(&b, "job_count         %d\n", d.JobCount)
	for i, j := range d.Jobs {
		fmt.Fprintf(&b, "job %d:\n", i+1)
		fmt.Fprintf(&b, "  database      %s\n", j.Database)
		fmt.Fprintf(&b, "  strategy      %s\n", j.Strategy)
		fmt.Fprintf(&b, "  compression   %s\n", j.Compression)
		fmt.Fprintf(&b, "  encryption    %s (%s)\n", j.Encryption, j.EncryptionMode)
		fmt.Fprintf(&b, "  storage       %s\n", j.Storage)
		fmt.Fprintf(&b, "  schedule      %s\n", j.Schedule)
		fmt.Fprintf(&b, "  maintenance   %t\n", j.Maintenance)
		fmt.Fprintf(&b, "  archive       %t\n", j.Archive)
		fmt.Fprintf(&b, "  successes     %d\n", j.Successes)
		fmt.Fprintf(&b, "  failures      %d\n", j.Failures)
		if j.RetentionTiers != "" {
			fmt.Fprintf(&b, "  retention     %s\n", j.RetentionTiers)
		}
		if j.DurationMs > 0 {
			fmt.Fprintf(&b, "  duration      %s\n", time.Duration(j.DurationMs)*time.Millisecond)
		}
		if j.Pruned > 0 {
			fmt.Fprintf(&b, "  pruned        %d\n", j.Pruned)
		}
		if j.Archived > 0 {
			fmt.Fprintf(&b, "  archived      %d\n", j.Archived)
		}
	}
	return b.String()
}

// parse integers and return 0 on junk
func atoiCode(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// parse int64 and return 0 on junk
func atoiCode64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func decodeBool(code string) bool {
	return code == flagYes
}

func decodeJobs(val string) []Job {
	var jobs []Job
	for _, entry := range strings.Split(val, "|") {
		f := strings.Split(entry, ":")
		if len(f) < 11 {
			continue
		}
		j := Job{
			Database:       decodeName(f[0], dbTypeNames),
			Strategy:       decodeName(f[1], strategyNames),
			Compression:    decodeName(f[2], compressionNames),
			Encryption:     decodeName(f[3], encryptionNames),
			EncryptionMode: decodeName(f[4], encryptionModeNames),
			Storage:        decodeName(f[5], storageNames),
			Schedule:       decodeName(f[6], scheduleNames),
			Maintenance:    decodeBool(f[7]),
			Archive:        decodeBool(f[8]),
			Successes:      atoiCode(f[9]),
			Failures:       atoiCode(f[10]),
		}
		if len(f) >= 12 {
			j.RetentionTiers = decodeTiers(f[11])
		}
		if len(f) >= 13 {
			j.DurationMs = atoiCode64(f[12])
		}
		if len(f) >= 14 {
			j.Pruned = atoiCode(f[13])
		}
		if len(f) >= 15 {
			j.Archived = atoiCode(f[14])
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func decodeLog(code string) Log {
	l := Log{}
	if len(code) > 0 {
		switch string(code[0]) {
		case logLevelDebug:
			l.Level = "debug"
		case logLevelInfo:
			l.Level = "info"
		case logLevelWarn:
			l.Level = "warn"
		case logLevelError:
			l.Level = "error"
		}
	}
	if len(code) > 1 {
		switch string(code[1]) {
		case logFmtText:
			l.Format = "text"
		case logFmtJSON:
			l.Format = "json"
		}
	}
	if len(code) > 2 {
		switch string(code[2]) {
		case logDestConsole:
			l.Destination = "console"
		case logDestFile:
			l.Destination = "file"
		case logDestBoth:
			l.Destination = "both"
		}
	}
	return l
}

// further translate code to full name
func decodeName(code string, table map[string]string) string {
	if name, ok := table[code]; ok {
		return name
	}
	if code == "" {
		return "unknown"
	}
	return "unknown (" + code + ")"
}

func decodeRuntime(code string) Runtime {
	r := Runtime{}
	if len(code) > 0 {
		switch string(code[0]) {
		case osLinux:
			r.OS = "linux"
		case osWindows:
			r.OS = "windows"
		case osDarwin:
			r.OS = "darwin"
		case osFreeBSD:
			r.OS = "freebsd"
		case osOpenBSD:
			r.OS = "openbsd"
		}
	}
	if len(code) > 1 {
		switch string(code[1]) {
		case archAmd64:
			r.Arch = "amd64"
		case archArm64:
			r.Arch = "arm64"
		case archArm:
			r.Arch = "arm"
		}
	}
	if len(code) > 2 {
		r.Container = decodeBool(string(code[2]))
	}
	if len(code) > 3 {
		r.Systemd = decodeBool(string(code[3]))
	}
	return r
}

// retention tier mask to a readable list, eg "hourly,daily" or "" when none
func decodeTiers(mask string) string {
	if mask == "" || mask == "0" {
		return ""
	}
	tiers := []string{"hourly", "daily", "weekly", "monthly", "yearly"}
	var out []string
	for i := 0; i < len(mask) && i < len(tiers); i++ {
		if mask[i] == flagYes[0] {
			out = append(out, tiers[i])
		}
	}
	return strings.Join(out, ",")
}

func toolName(code string) string {
	switch code {
	case ToolDBBackup:
		return "db-backup"
	default:
		return "unknown tool (" + code + ")"
	}
}

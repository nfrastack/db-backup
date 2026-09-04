// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/database"
	"github.com/nfrastack/db-backup/internal/storage"
)

const (
	FormatName    = "dbbackup.sidecar"
	SchemaVersion = 1
)

type Sidecar struct {
	Format          string            `json:"format"`
	SchemaVersion   int               `json:"schema_version"`
	Tool            *ToolMeta         `json:"tool"`
	Job             *JobMeta          `json:"job,omitempty"`
	Trigger         string            `json:"trigger,omitempty"`
	SessionID       string            `json:"session_id,omitempty"`
	RunID           string            `json:"run_id,omitempty"`
	Status          string            `json:"status,omitempty"`
	DurationMs      int64             `json:"duration_ms,omitempty"`
	RawSize         int64             `json:"raw_size,omitempty"`
	FileName        string            `json:"filename"`
	Strategy        string            `json:"strategy"`
	SchemaOnly      bool              `json:"schema_only,omitempty"`
	Tables          *TableMeta        `json:"tables,omitempty"`
	Notes           []string          `json:"notes,omitempty"`
	Base            string            `json:"base,omitempty"`
	Position        string            `json:"position,omitempty"`
	Type            string            `json:"type"`
	DB              string            `json:"db"`
	Host            string            `json:"host"`
	Timestamp       string            `json:"timestamp"`
	Checksums       map[string]string `json:"checksums"`
	Size            int64             `json:"size"`
	Compress        string            `json:"compress,omitempty"`
	CompressLevel   int               `json:"compression_level,omitempty"`
	CompressThreads int               `json:"compression_threads,omitempty"`
	Rsyncable       bool              `json:"rsyncable,omitempty"`
	ChainDepth      int               `json:"chain_depth,omitempty"`
	Encryption      *EncryptionMeta   `json:"encryption,omitempty"`
}

type TableMeta struct {
	Include    []string `json:"include,omitempty"`
	Exclude    []string `json:"exclude,omitempty"`
	SchemaOnly []string `json:"schema_only,omitempty"`
}

type ToolMeta struct {
	Name     string `json:"name"`
	Edition  string `json:"edition,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Version  string `json:"version"`
}

type JobMeta struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule,omitempty"`
}

type EncryptionMeta struct {
	Type       string   `json:"type"`
	Recipients []string `json:"recipients,omitempty"`
	Passphrase bool     `json:"passphrase,omitempty"`
	Key        string   `json:"key,omitempty"`
	Hash       string   `json:"hash,omitempty"`
}

var dumpFormatExts = []string{".sql", ".mongo", ".redis", ".influx", ".couch"}

func BuildFilename(dbType, dbName, host, strategy string) string {
	ts := TimestampString()
	return fmt.Sprintf("%s-%s-%s-%s-%s%s", dbType, dbName, host, strategy, ts, database.FormatExtension(dbType))
}
func ParseTimestamp(filename string) (time.Time, bool) {
	base := strings.TrimSuffix(filename, ".gpg")
	base = strings.TrimSuffix(base, ".zst")
	base = strings.TrimSuffix(base, ".gz")
	base = strings.TrimSuffix(base, ".bz2")
	base = strings.TrimSuffix(base, ".xz")
	base = stripFormatExt(base)
	parts := strings.Split(base, "-")
	if len(parts) >= 2 {
		candidate := parts[len(parts)-2] + "-" + parts[len(parts)-1]
		if t, err := time.Parse("20060102-150405", candidate); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
func ReadSidecar(st storage.Storage, filename string) (*Sidecar, error) {
	name := SidecarName(filename)
	rc, _, err := st.Download(context.Background(), name)
	if err != nil {
		return nil, fmt.Errorf("download sidecar %s: %w", name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read sidecar: %w", err)
	}
	var sc Sidecar
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, fmt.Errorf("parse sidecar: %w", err)
	}
	return &sc, nil
}
func SidecarName(filename string) string {
	return filename + ".json"
}

func StrategyFromFilename(filename string) string {

	base := filename
	for _, ext := range []string{".gpg", ".zst", ".gz", ".bz2", ".xz"} {
		base = strings.TrimSuffix(base, ext)
	}
	base = stripFormatExt(base)
	parts := strings.Split(base, "-")

	if len(parts) >= 6 {
		switch parts[3] {
		case "incr":
			return "incremental"
		case "diff":
			return "differential"
		case "full":
			return "full"
		}
	}
	return "full"
}

func StrategyMatches(filename, strategy string) bool {
	return StrategyFromFilename(filename) == strategy
}

func TimestampString() string {
	return time.Now().Format("20060102-150405")
}

func WriteSidecar(st storage.Storage, filename string, sc *Sidecar) error {
	data, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("marshal sidecar: %w", err)
	}
	name := SidecarName(filename)
	_, err = st.Upload(context.Background(), name, strings.NewReader(string(data)))
	return err
}

func stripFormatExt(s string) string {
	for _, ext := range dumpFormatExts {
		s = strings.TrimSuffix(s, ext)
	}
	return s
}

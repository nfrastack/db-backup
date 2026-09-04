// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type BackupInfo struct {
	Type       string
	DBName     string
	Host       string
	Strategy   string
	Timestamp  time.Time
	Path       string
	Size       int64
	Compress   string
	Encryption string
}

var backupRe = regexp.MustCompile(`^(.+?)-(.+?)-(.+?)-(full|incr|diff)-(\d{8}-\d{6})(?:-\d+)?\.(?:sql|mongo|redis|influx|couch)(\.(zst|gz|bz2|xz))?(\.(age|enc|gpg))?$`)
var backupReIP = regexp.MustCompile(`^(.+?)-(.+?)-(\d+_\d+_\d+_\d+)-(full|incr|diff)-(\d{8}-\d{6})(?:-\d+)?\.(?:sql|mongo|redis|influx|couch)(\.(zst|gz|bz2|xz))?(\.(age|enc|gpg))?$`)

var extToCompress = map[string]string{
	".bz2": "bzip2",
	".gz":  "gzip",
	".xz":  "xz",
	".zst": "zstd",
}

var extToEncryption = map[string]string{
	".age": "age",
	".enc": "openssl",
	".gpg": "gpg",
}

func (b *BackupInfo) DisplaySize() string {
	if b.Size < 1024 {
		return strconv.FormatInt(b.Size, 10) + "B"
	}
	if b.Size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(b.Size)/1024)
	}
	if b.Size < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(b.Size)/(1024*1024))
	}
	return fmt.Sprintf("%.1fGB", float64(b.Size)/(1024*1024*1024))
}

func FilterBackupsByJob(entries []BackupInfo, jobType, jobHost string) []BackupInfo {
	var filtered []BackupInfo
	for _, e := range entries {
		if strings.EqualFold(e.Type, jobType) && e.Host == jobHost {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
func FormatExtension(dbType string) string {
	switch strings.ToLower(dbType) {
	case "couch", "couchdb":
		return ".couch"
	case "influx":
		return ".influx"
	case "mysql", "mariadb", "postgres", "pgsql", "postgresql", "sqlite", "sqlite3", "mssql", "microsoftsql":
		return ".sql"
	case "mongo", "mongodb":
		return ".mongo"
	case "redis":
		return ".redis"
	default:
		return ".sql"
	}
}

func IsBackupFile(path string) bool {
	_, err := ParseBackupFilename(path)
	return err == nil
}

func LatestBackup(entries []BackupInfo) *BackupInfo {
	if len(entries) == 0 {
		return nil
	}
	latest := &entries[0]
	for i := range entries {
		if entries[i].Timestamp.After(latest.Timestamp) {
			latest = &entries[i]
		}
	}
	return latest
}
func ParseBackupFilename(path string) (*BackupInfo, error) {
	base := filepath.Base(path)

	m := backupReIP.FindStringSubmatch(base)
	if m == nil {
		m = backupRe.FindStringSubmatch(base)
	}
	if m == nil {
		return nil, fmt.Errorf("cannot parse backup filename: %s", base)
	}

	ts, err := time.Parse("20060102-150405", m[5])
	if err != nil {
		return nil, fmt.Errorf("parse timestamp %q: %w", m[5], err)
	}

	strategy := "full"
	switch m[4] {
	case "incr":
		strategy = "incremental"
	case "diff":
		strategy = "differential"
	}

	compress := ""
	if m[7] != "" {
		compress = extToCompress["."+m[7]]
	}

	return &BackupInfo{
		Type:       m[1],
		DBName:     m[2],
		Host:       m[3],
		Strategy:   strategy,
		Timestamp:  ts,
		Path:       base,
		Compress:   compress,
		Encryption: extToEncryption[m[8]],
	}, nil
}

func UniqueBackupName(name string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	idx := strings.Index(name, ".")
	for n := 2; n < 1000; n++ {
		cand := name
		if idx > 0 {
			cand = name[:idx] + "-" + strconv.Itoa(n) + name[idx:]
		}
		if _, err := ParseBackupFilename(cand); err != nil {
			continue
		}
		if !taken(cand) {
			return cand
		}
	}
	return name
}

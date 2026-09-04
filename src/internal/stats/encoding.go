// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// encode values into payload
package stats

import (
	"github.com/nfrastack/db-backup/internal/compress"
	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/encrypt"
	"github.com/nfrastack/db-backup/internal/storage"
)

// tool code
const (
	ToolDBBackup = "322"
)
const SchemaVersion = 2

// db type codes jf1
const (
	dbCouch    = "c" // couch
	dbInflux   = "i" // influx
	dbMSSQL    = "s" // mssql
	dbMariaDB  = "a" // mariadb
	dbMongo    = "o" // mongo
	dbMySQL    = "m" // mysql
	dbPostgres = "p" // postgres
	dbRedis    = "r" // redis
	dbSQLite   = "q" // sqlite
	dbOther    = "?" // unknown
)

// backup strategy jf2
const (
	stratFull  = "f" // full
	stratIncr  = "n" // incremental
	stratDiff  = "d" // differential
	stratOther = "?" // unknown
)

// compression jf3
const (
	compNone  = "0" // none
	compGzip  = "g" // gzip
	compZstd  = "z" // zstd
	compXz    = "x" // xz
	compBz2   = "b" // bzip2
	compOther = "?" // unknown
)

// encryption jf4
const (
	encNone    = "0" // none
	encAge     = "e" // age
	encOpenPGP = "p" // openpgp / gpg
	encOpenSSL = "o" // openssl enc
	encOther   = "?" // unknown
)

// encryption mode jf5
const (
	encModeNone       = "0" // none
	encModeRecipients = "r" // public key / recipient
	encModePassphrase = "s" // ps / symmetric
	encModeIdentity   = "i" // age identity
)

// storage backend jf6
const (
	storeFilesystem = "f" // filesystem
	storeS3         = "s" // s3 compatible
	storeAzure      = "a" // azure blob
	storeGCS        = "g" // google cloud storage
	storeWebDAV     = "w" // webdav
	storeOther      = "?" // unknown
)

// schedule jf7 - cron is custom
const (
	schedManual  = "m" // manual / on demand
	schedHourly  = "h" // hourly
	schedDaily   = "d" // daily
	schedWeekly  = "w" // weekly
	schedMonthly = "o" // monthly
	schedCustom  = "x" // cron / interval
)

// retention tier jf12 - five digits "hourly daily weekly monthly yearly".
const (
	retentionNone = "0" // no GFS tiers configured
)

// runtime - 4 digit "os arch container systemd".
const (
	osLinux   = "1"
	osWindows = "2"
	osDarwin  = "3"
	osFreeBSD = "4"
	osOpenBSD = "5"

	archAmd64 = "1"
	archArm64 = "2"
	archArm   = "3"

	flagYes = "1"
	flagNo  = "0"
)

// log options - 3 digit "level format destination"
const (
	logLevelDebug = "1"
	logLevelInfo  = "2"
	logLevelWarn  = "3"
	logLevelError = "4"

	logFmtText = "1"
	logFmtJSON = "2"

	logDestConsole = "1"
	logDestFile    = "2"
	logDestBoth    = "3"
)

func compressionCode(t string) string {
	switch compress.Parse(t) {
	case compress.Bzip2:
		return compBz2
	case compress.Gzip:
		return compGzip
	case compress.Zstd:
		return compZstd
	case compress.XZip:
		return compXz
	case compress.None:
		return compNone
	default:
		return compOther
	}
}

func dbTypeCode(t string) string {
	switch normalize(t) {
	case "couch", "couchdb":
		return dbCouch
	case "influx", "influxdb":
		return dbInflux
	case "mysql":
		return dbMySQL
	case "mariadb":
		return dbMariaDB
	case "mongo", "mongodb":
		return dbMongo
	case "mssql", "microsoftsql":
		return dbMSSQL
	case "postgres", "postgresql", "pgsql":
		return dbPostgres
	case "redis":
		return dbRedis
	case "sqlite", "sqlite3":
		return dbSQLite
	default:
		return dbOther
	}
}

func encryptionCode(t string) string {
	switch encrypt.Parse(t) {
	case encrypt.Age:
		return encAge
	case encrypt.OpenPGP:
		return encOpenPGP
	case encrypt.OpenSSL:
		return encOpenSSL
	case encrypt.None:
		return encNone
	default:
		return encOther
	}
}

func encryptionModeCode(t string, identity string, identities []string) string {
	switch encrypt.Parse(t) {
	case encrypt.None:
		return encModeNone
	case encrypt.OpenSSL:
		return encModePassphrase
	case encrypt.Age:
		if identity != "" {
			return encModeIdentity
		}
		return encModeRecipients
	case encrypt.OpenPGP:
		if identity != "" || len(identities) == 0 {
			return encModePassphrase
		}
		return encModeRecipients
	default:
		return encModeNone
	}
}

func isDailyCron(f []string) bool {
	if f[2] != "*" || f[3] != "*" {
		return false
	}
	return f[0] != "*" && f[1] != "*" && f[4] == "*"
}

func isHourlyCron(f []string) bool {
	return f[0] != "*" && f[1] == "*" && f[2] == "*" && f[3] == "*" && f[4] == "*"
}

func isMonthlyCron(f []string) bool {
	if f[2] == "*" || f[3] != "*" {
		return false
	}
	return f[0] != "*" && f[1] != "*"
}

func isWeeklyCron(f []string) bool {
	if f[2] != "*" || f[3] != "*" {
		return false
	}
	return f[0] != "*" && f[1] != "*" && f[4] != "*" && f[4] != "0"
}

func normalize(s string) string {
	if s == "" {
		return ""
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c = c + ('a' - 'A')
		case c == ' ' || c == '-' || c == '_':
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

func retentionCode(ret *config.RetentionConfig) string {
	if ret == nil {
		return retentionNone
	}
	flag := func(n *int) string {
		if n != nil && *n > 0 {
			return flagYes
		}
		return flagNo
	}
	return flag(ret.Hourly) + flag(ret.Daily) + flag(ret.Weekly) + flag(ret.Monthly) + flag(ret.Yearly)
}

func scheduleCode(s *config.Schedule) string {
	if s == nil {
		return schedCustom
	}
	switch {
	case s.Cron != "":
		fields := splitFields(s.Cron)
		if len(fields) == 5 {
			switch {
			case isHourlyCron(fields):
				return schedHourly
			case isDailyCron(fields):
				return schedDaily
			case isWeeklyCron(fields):
				return schedWeekly
			case isMonthlyCron(fields):
				return schedMonthly
			}
		}
		return schedCustom
	case s.Interval > 0:
		switch {
		case s.Interval <= 60:
			return schedHourly
		case s.Interval <= 1440:
			return schedDaily
		case s.Interval <= 10080:
			return schedWeekly
		default:
			return schedMonthly
		}
	default:
		return schedCustom
	}
}

func splitFields(s string) []string {
	var out []string
	var cur []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t':
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
		default:
			cur = append(cur, s[i])
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func storageCode(b string) string {
	switch storage.Backend(normalize(b)) {
	case storage.Filesystem:
		return storeFilesystem
	case storage.S3:
		return storeS3
	case storage.Azure:
		return storeAzure
	case storage.GCS:
		return storeGCS
	case storage.WebDAV:
		return storeWebDAV
	default:
		return storeOther
	}
}
func strategyCode(s string) string {
	switch normalize(s) {
	case "full":
		return stratFull
	case "incremental", "incr":
		return stratIncr
	case "differential", "diff":
		return stratDiff
	default:
		return stratOther
	}
}

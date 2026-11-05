// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"runtime"
)

func ConfigPaths() []string {
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd":
		return []string{
			filepath.Join(home(), "db-backup.yml"),
			"/usr/local/etc/db-backup.yml",
			"/usr/local/etc/dbbackup.yml",
			"/etc/db-backup.yml",
			"/etc/dbbackup.yaml",
		}
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home(), "AppData", "Roaming")
		}
		return []string{
			filepath.Join(appdata, "dbb", "db-backup.yml"),
		}
	default:
		return []string{
			"/etc/db-backup.yaml",
			"/etc/db-backup.yml",
			"/etc/dbbackup.yaml",
			"/etc/dbbackup.yml",
		}
	}
}

// StateDir defaults
func DefaultStateDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home(), "Library", "Application Support", "dbb")
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "dbb")
		}
		return filepath.Join(home(), "AppData", "Roaming", "dbb")
	default:
		return "/var/lib/dbb"
	}
}

func StoragePath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home(), "Backups")
	case "windows":
		return "/backups"
	// linux, freebsd, openbsd
	default:
		return "/var/backups"
	}
}

func TempPath() string {
	return os.TempDir()
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "/"
	}
	return h
}

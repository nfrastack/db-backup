// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package license

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var pathMu sync.RWMutex
var forcedFile string
var stateDir string

func CandidatePaths() []string {
	pathMu.RLock()
	forced := forcedFile
	dir := stateDir
	pathMu.RUnlock()

	var paths []string
	if forced != "" {
		paths = append(paths, forced)
	}
	if v := os.Getenv("DBBACKUP_LICENSE"); v != "" {
		paths = append(paths, v)
	}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, "db-backup.lic"))
	}
	if dir != "" {
		paths = append(paths, filepath.Join(dir, "db-backup.lic"))
	}
	paths = append(paths,
		filepath.Join(os.Getenv("HOME"), ".local", "state", "dbb", "db-backup.lic"),
	)
	paths = append(paths, systemLicensePaths()...)
	return paths
}

func IsDiscoveryPath(p string) bool {
	clean := filepath.Clean(p)
	for _, c := range CandidatePaths() {
		if filepath.Clean(c) == clean {
			return true
		}
	}
	return false
}
func Load() (*License, error) {
	for _, p := range CandidatePaths() {
		raw, err := resolveLicenseValue(p, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if raw == "" {
			continue
		}
		raw, err = NormalizeArtifact(raw)
		if err != nil {
			return nil, fmt.Errorf("license %s: %w", p, err)
		}
		lic, err := Parse(raw)
		if err == nil {
			return lic, nil
		}
		return nil, err
	}
	return nil, nil
}

func SetLicenseFile(path string) {
	pathMu.Lock()
	forcedFile = strings.TrimSpace(path)
	pathMu.Unlock()
}

func SetStateDir(path string) {
	pathMu.Lock()
	stateDir = strings.TrimSpace(path)
	pathMu.Unlock()
}

func StateDir() string {
	pathMu.RLock()
	defer pathMu.RUnlock()
	return stateDir
}
func looksLikeArtifact(v string) bool {
	if v == "" || strings.ContainsAny(v, "/\\") {
		return false
	}
	i := strings.IndexByte(v, '.')
	if i <= 0 || strings.IndexByte(v[i+1:], '.') >= 0 {
		return false
	}
	if _, err := decodeB64(v[:i]); err != nil {
		return false
	}
	return true
}
func resolveLicenseValue(value string, depth int) (string, error) {
	if depth > 5 {
		return "", errors.New("license value resolution too deep")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	switch {
	case strings.HasPrefix(value, "data:"):
		return strings.TrimSpace(value[len("data:"):]), nil
	case strings.HasPrefix(value, "file://"):
		b, err := os.ReadFile(value[len("file://"):])
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	case strings.HasPrefix(value, "env://"):
		return resolveLicenseValue(os.Getenv(value[len("env://"):]), depth+1)
	default:
		if st, err := os.Stat(value); err == nil && !st.IsDir() {
			b, err := os.ReadFile(value)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(b)), nil
		}
		if looksLikeArtifact(value) {
			return value, nil
		}
		return "", nil
	}
}
func systemLicensePaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/usr/local/etc/db-backup.lic"}
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return []string{filepath.Join(appdata, "dbb", "db-backup.lic")}
	default:
		return []string{"/etc/db-backup.lic"}
	}
}

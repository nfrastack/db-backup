// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import "fmt"

type PhysicalRestoreLimits struct {
	ManifestMaxBytes int64 `yaml:"manifest_max_bytes"`
	MaxEntries       int   `yaml:"max_entries"`
	MaxFileBytes     int64 `yaml:"max_file_bytes"`
	MaxTotalBytes    int64 `yaml:"max_total_bytes"`
}

func DefaultPhysicalRestoreLimits() PhysicalRestoreLimits {
	return PhysicalRestoreLimits{
		ManifestMaxBytes: 64 << 20,
		MaxEntries:       100000,
		MaxFileBytes:     100 << 30,
		MaxTotalBytes:    1 << 40,
	}
}

func (l PhysicalRestoreLimits) WithDefaults() (PhysicalRestoreLimits, error) {
	defaults := DefaultPhysicalRestoreLimits()
	if l.ManifestMaxBytes == 0 {
		l.ManifestMaxBytes = defaults.ManifestMaxBytes
	}
	if l.MaxEntries == 0 {
		l.MaxEntries = defaults.MaxEntries
	}
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = defaults.MaxFileBytes
	}
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = defaults.MaxTotalBytes
	}
	if l.ManifestMaxBytes < 0 || l.MaxEntries < 0 || l.MaxFileBytes < 0 || l.MaxTotalBytes < 0 {
		return PhysicalRestoreLimits{}, fmt.Errorf("physical restore limits must not be negative")
	}
	return l, nil
}

func (r *RestoreConfig) ResolvePhysicalRestoreLimits() (PhysicalRestoreLimits, error) {
	if r == nil || r.PhysicalRestore == nil {
		return DefaultPhysicalRestoreLimits(), nil
	}
	return r.PhysicalRestore.WithDefaults()
}
